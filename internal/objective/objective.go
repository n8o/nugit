// Package objective builds the PR view's Objective section and its
// objective-focus check (ADR-0048).
//
// A commit names the bead it works toward with an `objective:` trailer. This
// package attributes the range's changed files to the objectives that touched
// them, resolves each objective against the plan store, and lists the live
// knowledge governing what it touched. Scope comes from the commits, never from
// a declaration made before the work. Pure over already-loaded inputs, like
// every other keystone check (ADR-0006).
package objective

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/evidence"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// MaxKnowledge caps the knowledge listed per objective. The rest is counted:
// the section frames the change, it is not a second context() bundle.
const MaxKnowledge = 8

// Check is the consistency check id this package emits.
const Check = "objective-focus"

// Input is everything the view is computed from.
type Input struct {
	// Commits are the range's commits, trailers parsed.
	Commits []model.Commit
	// Files returns the paths one commit changed (git-root-relative).
	Files func(sha string) []string
	Code  model.CodeDelta
	// Knowledge is this PR's knowledge delta; AllObjects is the store at head.
	Knowledge  model.KnowledgeDelta
	AllObjects []model.KnowledgeObject
	// Head and Base are the plan store at each ref; HasStore is false when
	// the repo keeps none, so an id cannot be checked against anything.
	Head, Base beads.Store
	HasStore   bool
}

// View returns the range's objectives, or nil when no commit names one, so a
// repo whose agents do not name objectives renders exactly as before.
func View(in Input) *model.ObjectiveView {
	var order []string
	commits := map[string]int{}
	files := map[string]map[string]bool{} // objective -> files its commits touched
	claimed := map[string]bool{}          // files touched by any objective commit
	var unnamed []string                  // commits that name no objective
	for _, c := range in.Commits {
		id := strings.TrimSpace(c.Trailer.Objective)
		if id == "" {
			unnamed = append(unnamed, c.SHA)
			continue
		}
		if _, ok := commits[id]; !ok {
			order = append(order, id)
			files[id] = map[string]bool{}
		}
		commits[id]++
		for _, p := range in.Files(c.SHA) {
			files[id][p] = true
			claimed[p] = true
		}
	}
	if len(order) == 0 {
		return nil
	}

	// Only files that are in the PR's net diff count: a file one commit added
	// and a later one removed is not part of the change.
	changed := map[string]model.FileChange{}
	for _, fc := range in.Code.Files {
		changed[fc.Path] = fc
	}
	touchedByPR := map[string]bool{}
	for _, kc := range in.Knowledge.Changes {
		if kc.Object != nil && kc.Object.ID != "" && (kc.Status == "A" || kc.Status == "M") {
			touchedByPR[kc.Object.ID] = true
		}
	}

	v := &model.ObjectiveView{}
	for _, id := range order {
		o := model.Objective{ID: id, Commits: commits[id]}
		var paths []string
		comps := map[string]bool{}
		for p := range files[id] {
			fc, ok := changed[p]
			if !ok {
				continue
			}
			paths = append(paths, p)
			if fc.Component != "" {
				comps[fc.Component] = true
			}
		}
		sort.Strings(paths)
		o.Files = len(paths)
		for c := range comps {
			o.Components = append(o.Components, c)
		}
		sort.Strings(o.Components)
		resolve(&o, in)
		o.Knowledge, o.KnowledgeMore = scoped(in.AllObjects, comps, paths, touchedByPR)
		v.Objectives = append(v.Objectives, o)
	}

	// A file is outside every objective when only unnamed commits touched it.
	outside := map[string]bool{}
	for _, sha := range unnamed {
		for _, p := range in.Files(sha) {
			if _, ok := changed[p]; ok && !claimed[p] {
				outside[p] = true
			}
		}
	}
	for p := range outside {
		v.Outside = append(v.Outside, p)
	}
	sort.Strings(v.Outside)
	return v
}

// resolve fills the bead's fields from the store at head and base.
func resolve(o *model.Objective, in Input) {
	it, ok := in.Head.Find(o.ID)
	if !ok {
		return
	}
	o.Known = true
	o.Title = it.Title
	o.Status = it.Status
	if b, ok := in.Base.Find(o.ID); ok {
		o.BaseStatus = b.Status
	}
	o.Acceptance = strings.TrimSpace(it.Field("acceptance_criteria", "acceptance"))
	o.Plan = it.Plan
	o.PlanDone, o.PlanTotal = beads.PlanProgress(in.Head, it.Plan)
}

// scoped lists the live local decisions and lessons that govern one of comps
// or apply to one of paths. Global objects are left out: they apply to every
// objective, so they say nothing about this one.
func scoped(objs []model.KnowledgeObject, comps map[string]bool, paths []string, touched map[string]bool) ([]model.ObjectiveRef, int) {
	var refs []model.ObjectiveRef
	for i := range objs {
		o := &objs[i]
		if o.Foreign() || o.ID == "" || (o.Type != model.KindDecision && o.Type != model.KindLesson) {
			continue
		}
		if st := o.EffectiveStatus; st == model.StatusSuperseded || st == model.StatusInvalidated {
			continue
		}
		if !governs(o, comps, paths) {
			continue
		}
		refs = append(refs, model.ObjectiveRef{ID: o.ID, Type: o.Type, Title: title(o.Body), New: touched[o.ID]})
	}
	// What this PR changed first, then decisions before lessons, then by id.
	sort.SliceStable(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.New != b.New {
			return a.New
		}
		if a.Type != b.Type {
			return a.Type == model.KindDecision
		}
		return a.ID < b.ID
	})
	if len(refs) > MaxKnowledge {
		return refs[:MaxKnowledge], len(refs) - MaxKnowledge
	}
	return refs, 0
}

func governs(o *model.KnowledgeObject, comps map[string]bool, paths []string) bool {
	for _, c := range evidence.GovernedComponents(*o) {
		if comps[c] {
			return true
		}
	}
	for _, p := range paths {
		if knowledge.AppliesTo(o, p) {
			return true
		}
	}
	return false
}

// title is the first markdown heading of a record's body, else its first line.
func title(body string) string {
	first := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
		if first == "" {
			first = line
		}
	}
	return first
}

// Findings is the objective-focus check over a computed view. It emits
// nothing for a nil view.
func Findings(v *model.ObjectiveView, hasStore bool) []model.Finding {
	if v == nil {
		return nil
	}
	var fs []model.Finding
	if len(v.Objectives) > 1 {
		ids := make([]string, len(v.Objectives))
		for i, o := range v.Objectives {
			ids[i] = o.ID
		}
		fs = append(fs, model.Finding{
			Check:    Check,
			Severity: model.SevWarn,
			Title:    fmt.Sprintf("this PR works toward %d objectives (%s)", len(ids), strings.Join(ids, ", ")),
			Detail:   "a PR should carry one objective; split the work so each PR's view describes one task",
		})
	}
	if n := len(v.Outside); n > 0 {
		fs = append(fs, model.Finding{
			Check:    Check,
			Severity: model.SevWarn,
			Title:    fmt.Sprintf("%d file(s) changed outside the objective", n),
			Detail: fmt.Sprintf("changed only by commits with no `objective:` trailer: %s; name the objective on those commits, or move the work to its own PR",
				strings.Join(trunc(v.Outside, 5), ", ")),
		})
	}
	if hasStore {
		for _, o := range v.Objectives {
			if !o.Known {
				fs = append(fs, model.Finding{
					Check:    Check,
					Severity: model.SevWarn,
					Title:    fmt.Sprintf("objective %s is not in the plan store", o.ID),
					Detail:   fmt.Sprintf("`objective: %s` names no bead at head; create the bead in this PR, or fix the id", o.ID),
				})
			}
		}
	}
	return fs
}

func trunc(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	return append(append([]string{}, ss[:n]...), fmt.Sprintf("+%d more", len(ss)-n))
}
