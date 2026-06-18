// Package consistency runs the cross-artifact checks that make the PR view
// *verify* rather than merely present (§9.3).
//
// Honesty about determinism (a review finding): only checks that reduce to set
// or graph operations over committed text live here. Each finding records WHY it
// fired so a human can audit a false positive instead of ripping the gate out.
package consistency

import (
	"fmt"
	"sort"
	"strings"

	"github.com/burrowfarm/nugit/internal/gitutil"
	"github.com/burrowfarm/nugit/internal/goimports"
	"github.com/burrowfarm/nugit/internal/knowledge"
	"github.com/burrowfarm/nugit/internal/mapping"
	"github.com/burrowfarm/nugit/internal/model"
	"github.com/burrowfarm/nugit/internal/trailers"
)

// Input bundles everything the checks need (all already computed).
type Input struct {
	Repo       gitutil.Repo
	RepoDir    string
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
}

// C4CodeFindings runs only the C4<->code check. The orchestrator calls this
// first because its result feeds the significance verdict, which in turn gates
// decision-coverage — running it separately avoids a dependency cycle.
func C4CodeFindings(in Input) []model.Finding { return Sort(checkC4Code(in)) }

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
	if in.Mapper.Empty() || in.Module == "" {
		return nil
	}
	type edge struct{ src, dst string }
	seen := map[edge]bool{}
	var fs []model.Finding
	for _, fc := range in.Code.Files {
		if fc.Status == "D" || fc.Component == "" {
			continue
		}
		dirs, _ := goimports.InternalDirs(in.RepoDir, in.Module, fc.Path)
		for _, dir := range dirs {
			dst := in.Mapper.ResolveDir(dir)
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
					Severity: model.SevFail,
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
	touchedComps := map[string]bool{}
	for _, fc := range in.Code.Files {
		if fc.Component != "" {
			touchedComps[fc.Component] = true
		}
	}
	var fs []model.Finding
	for comp := range touchedComps {
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
	if !strings.HasPrefix(strings.ToUpper(base), "SPEC-") {
		return ""
	}
	parts := strings.SplitN(base, "-", 3)
	if len(parts) >= 2 {
		return "SPEC-" + parts[1]
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
