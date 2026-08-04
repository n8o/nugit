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
	"github.com/n8o/nugit/internal/evidence"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/goimports"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/modelfacts"
	"github.com/n8o/nugit/internal/pyimports"
	"github.com/n8o/nugit/internal/trailers"
	"github.com/n8o/nugit/internal/tsdeps"
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
	// Recurrence configures the recurrence check (ADR-0019); zero value disables.
	Recurrence RecurrenceOpts
}

// C4CodeFindings runs the C4<->code check plus model-health checks. The
// orchestrator calls this first because the C4<->code result feeds the
// significance verdict, which in turn gates decision-coverage — running it
// separately avoids a dependency cycle.
func C4CodeFindings(in Input) []model.Finding {
	fs := checkC4Code(in)
	fs = append(fs, checkCMakeCode(in)...)
	fs = append(fs, checkPythonCode(in)...)
	fs = append(fs, checkTSCode(in)...)
	fs = append(fs, checkModelHealth(in)...)
	fs = append(fs, checkModelDrift(in)...)
	return Sort(fs)
}

// IsUndeclaredEdge reports whether a finding is a genuine code-introduced
// cross-component dependency the model doesn't declare (any language), as opposed
// to a model-health warning.
func IsUndeclaredEdge(f model.Finding) bool {
	switch f.Check {
	case "c4<->code", "cmake<->code", "python<->code", "ts<->code":
		return true
	}
	return false
}

// flagDirEdges maps a language's directory dependency edges onto component edges
// and flags those the model doesn't declare, for components the PR touched. The
// single enforcement core shared by CMake/Python/TS (Go has its own per-file path).
func flagDirEdges(in Input, check, detailFmt string, edges [][2]string) []model.Finding {
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
	for _, e := range edges {
		src := in.Mapper.ResolveDir(in.Prefix + e[0])
		dst := in.Mapper.ResolveDir(in.Prefix + e[1])
		if src == "" || dst == "" || in.HeadModel.SameLineage(src, dst) || !touched[src] {
			continue
		}
		if in.HeadModel.Covered(src, dst) {
			continue
		}
		k := [2]string{src, dst}
		if seen[k] {
			continue
		}
		seen[k] = true
		detail := fmt.Sprintf(detailFmt, src, dst)
		if a, b := containerScope(in.HeadModel, src), containerScope(in.HeadModel, dst); a != "" && b != "" && a != b && (a != src || b != dst) {
			detail += fmt.Sprintf(" (container-level alternative: declare `%s -> %s`)", a, b)
		}
		fs = append(fs, model.Finding{
			Check:    check,
			Severity: sev,
			Title:    fmt.Sprintf("undeclared dependency %s → %s", src, dst),
			Detail:   detail,
		})
	}
	return fs
}

// containerScope is the enforcement scope of an element id: itself when it is
// a container, its parent container when it is a component, "" when flat.
func containerScope(m model.Model, id string) string {
	if _, ok := m.Container(id); ok {
		return id
	}
	return m.ContainerOf(id)
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
	return flagDirEdges(in, "cmake<->code",
		"CMake links %s → %s via target_link_libraries, but workspace.dsl declares no such relationship; add it or remove the link",
		cg.Edges)
}

// checkPythonCode flags Python imports between components the model doesn't
// declare. Derived from the working tree (== head in CI); changed-file detection
// is still ref-correct via the code delta.
func checkPythonCode(in Input) []model.Finding {
	if in.Mapper.Empty() || in.HeadModel.Structural() || !pyimports.Detect(in.RepoDir) {
		return nil
	}
	pg, err := pyimports.Discover(in.RepoDir)
	if err != nil {
		return nil
	}
	return flagDirEdges(in, "python<->code",
		"Python imports %s → %s, but workspace.dsl declares no such relationship; add it or remove the import",
		pg.Edges)
}

// checkTSCode flags TypeScript/JS imports the model doesn't declare, via
// dependency-cruiser. When it isn't installed, emit one info finding (TS edges
// unenforced) rather than silently passing.
func checkTSCode(in Input) []model.Finding {
	if in.Mapper.Empty() || in.HeadModel.Structural() || !tsdeps.Detect(in.RepoDir) {
		return nil
	}
	if !tsdeps.Available() {
		return []model.Finding{{
			Check:    "ts<->code",
			Severity: model.SevInfo,
			Title:    "TypeScript architecture unenforced",
			Detail:   "dependency-cruiser is not installed, so TS import edges aren't checked; `npm i -g dependency-cruiser` to enforce them",
		}}
	}
	tg, _, err := tsdeps.Discover(in.RepoDir)
	if err != nil {
		return nil
	}
	return flagDirEdges(in, "ts<->code",
		"TypeScript imports %s → %s, but workspace.dsl declares no such relationship; add it or remove the import",
		tg.Edges)
}

// checkModelHealth surfaces authoring errors in workspace.dsl that would
// otherwise fail silently: duplicate element ids (components and containers
// share one namespace; last-wins collapse), invalid path globs (which never
// match anything), and relationship endpoints naming no declared element.
func checkModelHealth(in Input) []model.Finding {
	var fs []model.Finding
	compCounts := map[string]int{}
	for _, c := range in.HeadModel.Components {
		compCounts[c.ID]++
	}
	ctCounts := map[string]int{}
	for _, ct := range in.HeadModel.Containers {
		ctCounts[ct.ID]++
	}
	idSet := map[string]bool{}
	for id := range compCounts {
		idSet[id] = true
	}
	for id := range ctCounts {
		idSet[id] = true
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		total := compCounts[id] + ctCounts[id]
		if total <= 1 {
			continue
		}
		if ctCounts[id] == 0 { // component-only duplicate: wording unchanged
			fs = append(fs, model.Finding{
				Check: "model-health", Severity: model.SevWarn,
				Title:  fmt.Sprintf("duplicate component id %q (%d declarations)", id, total),
				Detail: "workspace.dsl declares this id more than once; only the last binding is used — rename or merge them",
			})
			continue
		}
		fs = append(fs, model.Finding{
			Check: "model-health", Severity: model.SevWarn,
			Title:  fmt.Sprintf("duplicate element id %q (%d declarations)", id, total),
			Detail: "workspace.dsl declares this id more than once (components and containers share one namespace); only the last binding is used — rename or merge them",
		})
	}
	for _, bad := range in.Mapper.InvalidPatterns() {
		fs = append(fs, model.Finding{
			Check: "model-health", Severity: model.SevWarn,
			Title:  fmt.Sprintf("%s %q has an invalid path glob %q", bad.Kind, bad.Comp, bad.Pattern),
			Detail: fmt.Sprintf("this glob is syntactically invalid and matches no files, so the %s owns nothing", bad.Kind),
		})
	}
	// Invalid applies_to_paths globs on knowledge objects (ADR-0020): the
	// binding is dead — report it, never silently drop it.
	for _, bad := range knowledge.InvalidAppliesGlobs(in.AllObjects) {
		id := bad.ID
		if id == "" {
			id = bad.Path
		}
		fs = append(fs, model.Finding{
			Check: "model-health", Severity: model.SevWarn,
			Title:  fmt.Sprintf("knowledge object %q has an invalid applies_to_paths glob %q", id, bad.Pattern),
			Detail: fmt.Sprintf("this glob is syntactically invalid and matches no files, so the binding is dead — fix it in %s", bad.Path),
		})
	}
	// A relationship endpoint that names no declared element can never cover a
	// code dependency — the edge silently enforces nothing.
	unknown := map[string]bool{}
	for _, r := range in.HeadModel.Relationships {
		for _, end := range []string{r.Src, r.Dst} {
			if compCounts[end] == 0 && ctCounts[end] == 0 {
				unknown[end] = true
			}
		}
	}
	unknownIDs := make([]string, 0, len(unknown))
	for id := range unknown {
		unknownIDs = append(unknownIDs, id)
	}
	sort.Strings(unknownIDs)
	for _, id := range unknownIDs {
		fs = append(fs, model.Finding{
			Check: "model-health", Severity: model.SevWarn,
			Title:  fmt.Sprintf("relationship endpoint %q matches no component or container", id),
			Detail: "workspace.dsl declares a relationship touching this unknown element, so the edge covers nothing — fix the id or declare the element",
		})
	}
	return fs
}

// checkModelDrift (ADR-0021) diffs the deterministic unit inventory — the same
// detector facts that ground the nugit-model bootstrap — against workspace.dsl,
// scoped to units whose directories this PR touches. A detected buildable or
// deployable unit with no model element and no path mapping is drift: the model
// silently stopped covering real code (the pilot lost 11 units this way, three
// of them detector-visible BEFORE its last manual refresh). Always warn, never
// fail: the remediation is a model refresh, not a code change. Excluded from
// IsUndeclaredEdge, so it never feeds the significance verdict.
func checkModelDrift(in Input) []model.Finding {
	if in.Mapper.Empty() || in.HeadModel.Structural() {
		return nil
	}
	unmodeled := modelfacts.Unmodeled(modelfacts.Units(in.RepoDir),
		in.Prefix, in.Mapper.ResolveDir, elementNames(in.HeadModel))
	var fs []model.Finding
	for _, u := range unmodeled {
		dirPrefix := in.Prefix + u.Dir + "/"
		touched, mapped := false, false
		for _, fc := range in.Code.Files {
			if strings.HasPrefix(fc.Path, dirPrefix) {
				touched = true
				if fc.Component != "" {
					mapped = true // a finer glob owns part of the unit — not absence
				}
			}
		}
		if !touched || mapped {
			continue
		}
		fs = append(fs, model.Finding{
			Check:    "model-drift",
			Severity: model.SevWarn,
			Title:    fmt.Sprintf("detected unit %s is missing from workspace.dsl", u.Dir),
			Detail: fmt.Sprintf("this PR touches %s, which the %s detector identifies as a real unit (%s), "+
				"but no model element maps it — run the nugit-model skill to refresh the model, "+
				"or add a component/container stub with `properties { paths %q }`",
				u.Dir, u.Kind, u.Evidence, u.Dir+"/**"),
		})
	}
	return fs
}

// elementNames collects every element id and display name in the model, for
// the drift check's name-match escape (a declared-but-unbound element is a
// binding gap, not absence).
func elementNames(m model.Model) []string {
	var out []string
	for _, c := range m.Components {
		out = append(out, c.ID, c.Name)
	}
	for _, ct := range m.Containers {
		out = append(out, ct.ID, ct.Name)
	}
	return out
}

// OtherFindings runs the checks that depend on the significance verdict (set via
// in.Architectural) or are independent of the C4<->code result.
func OtherFindings(in Input) []model.Finding {
	var fs []model.Finding
	fs = append(fs, checkStaleKnowledge(in)...)
	fs = append(fs, checkDecisionCoverage(in)...)
	fs = append(fs, checkSpecLinkage(in)...)
	fs = append(fs, checkCaptureHygiene(in)...)
	fs = append(fs, checkProseSupersession(in)...)
	fs = append(fs, checkRecurrence(in)...)
	return Sort(fs)
}

// checkProseSupersession: a knowledge object this PR adds or modifies declares
// in prose ("Supersedes <id>") that it replaces a live store object, but
// carries no front-matter `supersedes:`/`amends:` edge — effective status is
// derived from edges only (ADR-0003), so retrieval would keep serving BOTH
// texts as live (ADR-0022). Scoped to PR-touched objects: pre-existing store
// drift is `nugit doctor`'s job, not every PR's.
func checkProseSupersession(in Input) []model.Finding {
	touched := map[string]bool{}
	for _, kc := range in.Knowledge.Changes {
		if kc.Object != nil && kc.Object.ID != "" && (kc.Status == "A" || kc.Status == "M") {
			touched[kc.Object.ID] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}
	var fs []model.Finding
	for _, p := range knowledge.ProseOnlySupersessions(in.AllObjects) {
		if !touched[p.ObjectID] {
			continue
		}
		fs = append(fs, model.Finding{
			Check:    "prose-supersession",
			Severity: model.SevWarn,
			Title:    fmt.Sprintf("%s supersedes %s in prose only", p.ObjectID, p.Target),
			Detail: fmt.Sprintf("%s (%s) says it supersedes %s, but declares no front-matter edge, so %s (%s) still serves as live; "+
				"add `supersedes: %s` (or `relates_to: [amends:%s]` for partial supersession) so EffectiveStatus updates",
				p.ObjectID, p.ObjectPath, p.Target, p.Target, p.TargetPath, p.Target, p.Target),
		})
	}
	return fs
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
	fs = append(fs, checkProseSupersession(in)...)
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
			// Same lineage (identity, or a container and its own child) is
			// containment, never a flaggable dependency.
			if dst == "" || in.HeadModel.SameLineage(fc.Component, dst) {
				continue
			}
			e := edge{fc.Component, dst}
			if seen[e] {
				continue
			}
			seen[e] = true
			if !in.HeadModel.Covered(e.src, e.dst) {
				fs = append(fs, model.Finding{
					Check:    "c4<->code",
					Severity: sev,
					Title:    fmt.Sprintf("undeclared dependency %s → %s", e.src, e.dst),
					Detail:   c4CodeDetail(in.HeadModel, e.src, e.dst, fc.Path),
				})
			}
		}
	}
	return fs
}

// c4CodeDetail words the c4<->code finding. Flat and same-container pairs keep
// the historical wording byte-for-byte; a cross-container pair also offers the
// container-level roll-up edge (component level recommended first).
func c4CodeDetail(m model.Model, src, dst, path string) string {
	a, b := containerScope(m, src), containerScope(m, dst)
	if a != "" && b != "" && a != b && (a != src || b != dst) {
		return fmt.Sprintf("code in %s now imports %s but workspace.dsl declares neither `%s -> %s` (component level) "+
			"nor `%s -> %s` (container level — covers every %s → %s component dependency); "+
			"add one to the model or remove the import (introduced via %s)",
			src, dst, src, dst, a, b, a, b, path)
	}
	return fmt.Sprintf("code in %s now imports %s but workspace.dsl has no `%s -> %s` relationship; "+
		"add the relationship to the model or remove the import (introduced via %s)",
		src, dst, src, dst, path)
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
// knowledge object without updating it. The governed surface is reached two
// ways: via governed components (scope/constrains edges, ADR-0002) and via a
// direct applies_to_paths binding (ADR-0020) — the latter needs no component,
// which is the point (infra paths the C4 model deliberately doesn't cover).
func checkStaleKnowledge(in Input) []model.Finding {
	governing := map[string][]model.KnowledgeObject{} // component id -> stale objects
	var pathGoverned []model.KnowledgeObject          // stale objects bound directly to paths
	for _, o := range in.AllObjects {
		if o.EffectiveStatus != model.StatusSuperseded && o.EffectiveStatus != model.StatusInvalidated {
			continue
		}
		for _, comp := range evidence.GovernedComponents(o) {
			governing[comp] = append(governing[comp], o)
		}
		if len(o.AppliesToPaths) > 0 {
			pathGoverned = append(pathGoverned, o)
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
	// Direct path bindings (ADR-0020): a changed file matching a stale
	// object's applies_to_paths counts as touching its governed surface.
	if len(pathGoverned) > 0 {
		paths := make([]string, 0, len(in.Code.Files))
		for _, fc := range in.Code.Files {
			paths = append(paths, fc.Path)
		}
		sort.Strings(paths) // deterministic: first matching path (sorted) wins the Title
		for _, p := range paths {
			for i := range pathGoverned {
				o := &pathGoverned[i]
				if touchedKnowledge[o.ID] || reported[o.ID] || !knowledge.AppliesTo(o, p) {
					continue
				}
				reported[o.ID] = true
				fs = append(fs, model.Finding{
					Check:    "stale-knowledge",
					Severity: model.SevWarn,
					Title:    fmt.Sprintf("touches %s, governed by %s %s", p, o.EffectiveStatus, o.ID),
					Detail: fmt.Sprintf("%s (%s) is %s and applies to %s via applies_to_paths; this PR changes that file without updating it",
						o.ID, o.Path, o.EffectiveStatus, p),
				})
			}
		}
	}
	return fs
}

// checkDecisionCoverage: an architecturally-significant change with no
// accompanying or linked decision record. A `status: proposed` ADR is a
// candidate, not yet ratified knowledge (ADR-0016), so it softens the finding
// but does not satisfy it; the `decision:` trailer bypass stays — the trailer
// is the ADR-0005 capture primitive and records the why regardless of lane.
func checkDecisionCoverage(in Input) []model.Finding {
	if !in.Architectural {
		return nil
	}
	var proposed []string
	for _, kc := range in.Knowledge.Changes {
		if kc.Object != nil && kc.Object.Type == model.KindDecision &&
			(kc.Status == "A" || kc.Status == "M") {
			if kc.Object.Status == model.StatusProposed {
				proposed = append(proposed, kc.Object.ID)
				continue
			}
			return nil // covered by a ratified decision
		}
	}
	// also covered if a commit carries a decision: trailer
	for _, c := range in.Commits {
		if strings.TrimSpace(c.Trailer.Decision) != "" {
			return nil
		}
	}
	if len(proposed) > 0 {
		sort.Strings(proposed)
		return []model.Finding{{
			Check:    "decision-coverage",
			Severity: model.SevWarn,
			Title:    "architectural change covered only by a proposed (unratified) decision",
			Detail:   "the why is drafted (" + strings.Join(proposed, ", ") + ") but not ratified; promote it with `nugit ratify <id>` or add a `decision:` trailer",
		}}
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
