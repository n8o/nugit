// Package consistency runs the cross-artifact checks that make the PR view
// *verify* rather than merely present (§9.3).
//
// Honesty about determinism (a review finding): only checks that reduce to set
// or graph operations over committed text live here. Each finding records WHY it
// fired so a human can audit a false positive instead of ripping the gate out.
package consistency

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/cmake"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/goimports"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/trailers"
)

// Input bundles everything the checks need (all already computed).
type Input struct {
	Repo    gitutil.Repo
	RepoDir string
	Head    string // ref whose source the import graph is read from
	// Prefix is the nugit/module root within the git repo ("" when they coincide);
	// it bridges module-relative import dirs onto the git-root-relative globs.
	Prefix     string
	Module     string
	HeadModel  model.Model
	Mapper     *mapping.Mapper
	Code       model.CodeDelta
	C4         model.C4Delta
	Knowledge  model.KnowledgeDelta
	AllObjects []model.KnowledgeObject // at head
	Commits    []model.Commit
	// Architectural is the significance verdict (drives decision-coverage).
	Architectural bool
	// C4Warn downgrades the c4<->code check from fail to warn (warn-until-ratified
	// adoption mode; set from config c4.mode).
	C4Warn bool
}

// C4CodeFindings runs the C4<->code check plus model-health checks. The
// orchestrator calls this first because the C4<->code result feeds the
// significance verdict, which in turn gates decision-coverage — running it
// separately avoids a dependency cycle.
func C4CodeFindings(in Input) []model.Finding {
	fs := append(checkC4Code(in), checkCMakeCode(in)...)
	return Sort(append(fs, checkModelHealth(in)...))
}

// IsUndeclaredEdge reports whether a finding is a genuine code-introduced
// cross-component dependency the model doesn't declare (Go or CMake), as opposed
// to a model-health warning.
func IsUndeclaredEdge(f model.Finding) bool {
	return f.Check == "c4<->code" || f.Check == "cmake<->code"
}

// checkCMakeCode is the C++ analogue of checkC4Code: it re-derives the CMake
// target_link_libraries graph at the reviewed tree and flags any link between
// two components that workspace.dsl does not declare, for components the PR
// touched. Components/edges come from the SAME cmake analyzer the model was
// bootstrapped from, so a synced model is green by construction.
func checkCMakeCode(in Input) []model.Finding {
	if in.Mapper.Empty() || in.HeadModel.Structural() {
		return nil
	}
	// Read CMakeLists.txt at the REVIEWED ref (not the working tree), like the Go
	// check — so the C++ graph matches base..head.
	files := cmakeFilesAt(in.Repo, in.Head, in.Prefix)
	if len(files) == 0 {
		return nil // not a CMake project at this ref
	}
	cg := cmake.DiscoverFiles(files)
	if len(cg.Edges) == 0 {
		return nil
	}
	touched := map[string]bool{}
	for _, fc := range in.Code.Files {
		if fc.Component != "" {
			touched[fc.Component] = true
		}
	}
	sev := model.SevFail
	if in.C4Warn {
		sev = model.SevWarn
	}
	seen := map[[2]string]bool{}
	var fs []model.Finding
	for _, e := range cg.Edges {
		src := in.Mapper.ResolveDir(in.Prefix + e[0])
		dst := in.Mapper.ResolveDir(in.Prefix + e[1])
		if src == "" || dst == "" || src == dst || !touched[src] {
			continue
		}
		if in.HeadModel.HasRelationship(src, dst) {
			continue
		}
		k := [2]string{src, dst}
		if seen[k] {
			continue
		}
		seen[k] = true
		fs = append(fs, model.Finding{
			Check:    "cmake<->code",
			Severity: sev,
			Title:    fmt.Sprintf("undeclared dependency %s → %s", src, dst),
			Detail: fmt.Sprintf("CMake links %s → %s via target_link_libraries, but workspace.dsl declares no such relationship; "+
				"add the relationship to the model or remove the link", src, dst),
		})
	}
	return fs
}

// checkModelHealth surfaces authoring errors in workspace.dsl that would
// otherwise fail silently: duplicate component ids (last-wins collapse) and
// invalid path globs (which never match anything).
func checkModelHealth(in Input) []model.Finding {
	var fs []model.Finding
	counts := map[string]int{}
	for _, c := range in.HeadModel.Components {
		counts[c.ID]++
	}
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if counts[id] > 1 {
			fs = append(fs, model.Finding{
				Check: "model-health", Severity: model.SevWarn,
				Title:  fmt.Sprintf("duplicate component id %q (%d declarations)", id, counts[id]),
				Detail: "workspace.dsl declares this id more than once; only the last binding is used — rename or merge them",
			})
		}
	}
	for _, bad := range in.Mapper.InvalidPatterns() {
		fs = append(fs, model.Finding{
			Check: "model-health", Severity: model.SevWarn,
			Title:  fmt.Sprintf("component %q has an invalid path glob %q", bad.Comp, bad.Pattern),
			Detail: "this glob is syntactically invalid and matches no files, so the component owns nothing",
		})
	}
	return fs
}

// OtherFindings runs the checks that depend on the significance verdict (set via
// in.Architectural) or are independent of the C4<->code result.
func OtherFindings(in Input) []model.Finding {
	var fs []model.Finding
	fs = append(fs, checkStaleKnowledge(in)...)
	fs = append(fs, checkDecisionCoverage(in)...)
	fs = append(fs, checkSpecLinkage(in)...)
	fs = append(fs, checkCaptureHygiene(in)...)
	return Sort(fs)
}

// checkCaptureHygiene surfaces commits whose trailer block is missing a mandatory
// field (§6.1). Informational — capture is opt-in, so this nudges rather than
// blocks (ADR-0005: trailers are a signal, not the durable store).
func checkCaptureHygiene(in Input) []model.Finding {
	var fs []model.Finding
	for _, c := range in.Commits {
		for _, w := range trailers.Validate(c.Trailer) {
			fs = append(fs, model.Finding{
				Check:    "capture-hygiene",
				Severity: model.SevInfo,
				Title:    fmt.Sprintf("commit %s: %s", short(c.SHA), w),
				Detail:   "a trailer block is present but missing a mandatory field; add it or drop the block",
			})
		}
	}
	return fs
}

// Check runs all checks and returns findings sorted for stable output. It is a
// convenience for callers that already set in.Architectural.
func Check(in Input) []model.Finding {
	fs := append(checkC4Code(in), checkStaleKnowledge(in)...)
	fs = append(fs, checkDecisionCoverage(in)...)
	fs = append(fs, checkSpecLinkage(in)...)
	fs = append(fs, checkCaptureHygiene(in)...)
	return Sort(fs)
}

func Sort(fs []model.Finding) []model.Finding {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Severity != fs[j].Severity {
			return sevRank(fs[i].Severity) < sevRank(fs[j].Severity)
		}
		if fs[i].Check != fs[j].Check {
			return fs[i].Check < fs[j].Check
		}
		return fs[i].Title < fs[j].Title
	})
	return fs
}

func sevRank(s model.Severity) int {
	switch s {
	case model.SevFail:
		return 0
	case model.SevWarn:
		return 1
	default:
		return 2
	}
}

// checkC4Code: a changed Go file induces an import edge between two C4
// components that the model does not declare. This is the headline check and
// the bootstrapping spike — it runs against nugit's own import graph.
func checkC4Code(in Input) []model.Finding {
	// A structural model declares no relationships on purpose — never grade it.
	if in.Mapper.Empty() || in.Module == "" || in.HeadModel.Structural() {
		return nil
	}
	type edge struct{ src, dst string }
	seen := map[edge]bool{}
	sev := model.SevFail
	if in.C4Warn {
		sev = model.SevWarn
	}
	var fs []model.Finding
	for _, fc := range in.Code.Files {
		if fc.Status == "D" || fc.Component == "" {
			continue
		}
		// Read the file at the reviewed ref (not the working tree) so the import
		// graph matches base..head; skip _test.go and build-excluded files.
		src, _ := in.Repo.ShowFile(in.Head, fc.Path)
		dirs, included := goimports.Analyze(in.Module, fc.Path, src)
		if !included {
			continue
		}
		for _, dir := range dirs {
			// import dirs are module-relative; globs are git-root-relative.
			dst := in.Mapper.ResolveDir(in.Prefix + dir)
			if dst == "" || dst == fc.Component {
				continue
			}
			e := edge{fc.Component, dst}
			if seen[e] {
				continue
			}
			seen[e] = true
			if !in.HeadModel.HasRelationship(e.src, e.dst) {
				fs = append(fs, model.Finding{
					Check:    "c4<->code",
					Severity: sev,
					Title:    fmt.Sprintf("undeclared dependency %s → %s", e.src, e.dst),
					Detail: fmt.Sprintf("code in %s now imports %s but workspace.dsl has no `%s -> %s` relationship; "+
						"add the relationship to the model or remove the import (introduced via %s)",
						e.src, e.dst, e.src, e.dst, fc.Path),
				})
			}
		}
	}
	return fs
}

// cmakeFilesAt reads every CMakeLists.txt under the nugit root (prefix) at ref,
// returning them with nugit-root-relative directories.
func cmakeFilesAt(repo gitutil.Repo, ref, prefix string) []cmake.File {
	paths, err := repo.ListTree(ref)
	if err != nil {
		return nil
	}
	nugitRoot := strings.TrimSuffix(prefix, "/") // "" or e.g. "apps/op"
	var files []cmake.File
	for _, p := range paths {
		if path.Base(p) != "CMakeLists.txt" {
			continue
		}
		dir := path.Dir(p) // git-root-relative
		if nugitRoot != "" && dir != nugitRoot && !strings.HasPrefix(dir, nugitRoot+"/") {
			continue
		}
		rel := dir
		if nugitRoot != "" {
			rel = strings.TrimPrefix(strings.TrimPrefix(dir, nugitRoot), "/")
		}
		if rel == "" {
			rel = "."
		}
		src, _ := repo.ShowFile(ref, p)
		files = append(files, cmake.File{Dir: rel, Text: src})
	}
	return files
}

// checkStaleKnowledge: the PR changes code governed by a superseded/invalidated
// knowledge object without updating it.
func checkStaleKnowledge(in Input) []model.Finding {
	governing := map[string][]model.KnowledgeObject{} // component id -> stale objects
	for _, o := range in.AllObjects {
		if o.EffectiveStatus != model.StatusSuperseded && o.EffectiveStatus != model.StatusInvalidated {
			continue
		}
		for _, comp := range governedComponents(o) {
			governing[comp] = append(governing[comp], o)
		}
	}
	touchedKnowledge := map[string]bool{}
	for _, kc := range in.Knowledge.Changes {
		if kc.Object != nil {
			touchedKnowledge[kc.Object.ID] = true
		}
	}
	reported := map[string]bool{}
	touchedSet := map[string]bool{}
	for _, fc := range in.Code.Files {
		if fc.Component != "" {
			touchedSet[fc.Component] = true
		}
	}
	touchedComps := make([]string, 0, len(touchedSet))
	for c := range touchedSet {
		touchedComps = append(touchedComps, c)
	}
	sort.Strings(touchedComps) // deterministic: first component (sorted) wins the Title
	var fs []model.Finding
	for _, comp := range touchedComps {
		for _, o := range governing[comp] {
			if touchedKnowledge[o.ID] || reported[o.ID] {
				continue
			}
			reported[o.ID] = true
			fs = append(fs, model.Finding{
				Check:    "stale-knowledge",
				Severity: model.SevWarn,
				Title:    fmt.Sprintf("touches %s, governed by %s %s", comp, o.EffectiveStatus, o.ID),
				Detail: fmt.Sprintf("%s (%s) is %s and governs %s; this PR changes that code without updating it (%s)",
					o.ID, o.Path, o.EffectiveStatus, comp, o.Path),
			})
		}
	}
	return fs
}

// governedComponents returns the C4 component ids an object governs, via its
// scope and via constrains:/affects:/governs: edges.
func governedComponents(o model.KnowledgeObject) []string {
	set := map[string]bool{}
	if o.Scope != "" && o.Scope != "global" {
		set[o.Scope] = true
	}
	for _, e := range o.RelatesTo {
		edge := knowledge.ParseEdge(e)
		switch edge.Relation {
		case "constrains", "affects", "governs":
			set[edge.Target] = true
		}
	}
	var out []string
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// checkDecisionCoverage: an architecturally-significant change with no
// accompanying or linked decision record.
func checkDecisionCoverage(in Input) []model.Finding {
	if !in.Architectural {
		return nil
	}
	for _, kc := range in.Knowledge.Changes {
		if kc.Object != nil && kc.Object.Type == model.KindDecision &&
			(kc.Status == "A" || kc.Status == "M") {
			return nil // covered
		}
	}
	// also covered if a commit carries a decision: trailer
	for _, c := range in.Commits {
		if strings.TrimSpace(c.Trailer.Decision) != "" {
			return nil
		}
	}
	return []model.Finding{{
		Check:    "decision-coverage",
		Severity: model.SevWarn,
		Title:    "architectural change with no decision record",
		Detail:   "this PR is architecturally significant (see significance reasons) but adds no ADR and no `decision:` trailer; record the why",
	}}
}

// checkSpecLinkage (light): a commit claims a spec that has no matching spec
// object in-tree. The "criteria actually met" half is intentionally NOT claimed
// here — see docs/decisions and §9.3 honesty note.
func checkSpecLinkage(in Input) []model.Finding {
	specIDs := map[string]bool{}
	for _, o := range in.AllObjects {
		if o.Type == model.KindSpec && o.ID != "" {
			specIDs[strings.ToUpper(o.ID)] = true
		}
	}
	// also accept spec ids embedded in the spec filename (SPEC-014-...)
	for _, o := range in.AllObjects {
		if o.Type == model.KindSpec {
			if id := specIDFromPath(o.Path); id != "" {
				specIDs[strings.ToUpper(id)] = true
			}
		}
	}
	reported := map[string]bool{}
	var fs []model.Finding
	for _, c := range in.Commits {
		spec := strings.TrimSpace(c.Trailer.Spec)
		if spec == "" || reported[spec] {
			continue
		}
		if !specIDs[strings.ToUpper(spec)] {
			reported[spec] = true
			fs = append(fs, model.Finding{
				Check:    "spec-linkage",
				Severity: model.SevWarn,
				Title:    fmt.Sprintf("references unknown spec %s", spec),
				Detail:   fmt.Sprintf("commit %s claims `spec: %s` but no matching spec object exists under .nugit/**/specs/", short(c.SHA), spec),
			})
		}
	}
	return fs
}

func specIDFromPath(p string) string {
	base := p[strings.LastIndexByte(p, '/')+1:]
	base = strings.TrimSuffix(base, filepath.Ext(base)) // drop ".md"
	if !strings.HasPrefix(strings.ToUpper(base), "SPEC-") {
		return ""
	}
	parts := strings.SplitN(base, "-", 3)
	if len(parts) >= 2 {
		// keep only the leading alphanumeric run of the id segment
		num := parts[1]
		for i, r := range num {
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				num = num[:i]
				break
			}
		}
		return "SPEC-" + num
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
