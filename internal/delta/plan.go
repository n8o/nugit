package delta

import (
	"sort"
	"strconv"
	"strings"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
	"gopkg.in/yaml.v3"
)

// PlanScope selects which of a store's plans a PR renders.
type PlanScope string

const (
	// ScopeTouched renders only the plans this PR moved — the default. A Beads
	// store is repo-wide and holds every plan every agent is executing; without
	// this a PR that closes one step of one plan renders the whole board as its
	// own position, and the reader cannot tell which part of it this change is
	// responsible for (ADR-0040).
	ScopeTouched PlanScope = "touched"
	// ScopeAll renders the whole store — the repo's board, not this PR's work.
	ScopeAll PlanScope = "all"
)

// DiffPlan reads the plan at base and head and returns the head position
// annotated with what moved (newly completed/started, added/removed items),
// scoped to the plans this PR touched unless scope is ScopeAll.
func DiffPlan(repo gitutil.Repo, base, head, prefix string, scope PlanScope) model.PlanPosition {
	if hs, ok := beads.Read(repo, head, prefix); ok {
		bs, _ := beads.Read(repo, base, prefix)
		return diffStores(bs, hs, scope)
	}
	// plan.yml stand-in: one implicit plan, so scoping has nothing to select.
	h := planFile_(repo, head, prefix)
	if !h.Present {
		return h
	}
	b := planFile_(repo, base, prefix)
	return diffPlans(b, h)
}

// diffStores computes the head position over every plan, diffs each against
// base, and then keeps only the plans that moved (ScopeTouched).
//
// Scoping happens AFTER the diff, never before: which plans this PR touched is
// itself a fact about the diff, so there is no cheaper order — and computing
// the full position first is what lets the note say honestly how many plans it
// is not showing.
func diffStores(bs, hs beads.Store, scope PlanScope) model.PlanPosition {
	h := beads.Position(hs, nil)
	b := beads.Position(bs, nil)
	annotate(&h, b)
	if scope == ScopeAll {
		return h
	}
	touched := map[string]bool{}
	for _, t := range h.Tracks {
		if t.Changed() {
			touched[t.Name] = true
		}
	}
	if len(touched) == 0 {
		// Nothing moved. Rendering the board here would be exactly the failure
		// this scoping exists to end, so render no track at all and let the
		// note (and the plan-movement check) say what happened.
		out := model.PlanPosition{Present: true, PlansTotal: h.PlansTotal}
		for _, t := range h.Tracks {
			out.Hidden = append(out.Hidden, t.Name)
		}
		out.Note = noMovementNote(out)
		return out
	}
	scoped := beads.Position(hs, touched)
	annotate(&scoped, b)
	return scoped
}

func noMovementNote(p model.PlanPosition) string {
	n := len(p.Hidden)
	switch n {
	case 0:
		return "via Beads — the store holds no plan steps"
	case 1:
		return "via Beads — this PR moves no plan step; 1 plan in flight (" + p.Hidden[0] + "), not shown"
	default:
		return "via Beads — this PR moves no plan step; " + strconv.Itoa(n) + " plans in flight, none of them this PR's"
	}
}

// annotate fills the base-relative movement on the position and on each track.
func annotate(h *model.PlanPosition, b model.PlanPosition) {
	if !b.Present {
		return // nothing to diff against (plan introduced in this PR)
	}
	baseByPlan := map[string]model.PlanTrack{}
	for _, t := range b.Tracks {
		baseByPlan[t.Name] = t
	}
	for i := range h.Tracks {
		diffTrack(&h.Tracks[i], baseByPlan[h.Tracks[i].Name])
	}
	*h = withTrackRollup(*h, b)
}

// diffTrack annotates one plan's track against its own base state.
func diffTrack(h *model.PlanTrack, b model.PlanTrack) {
	baseDone := set(b.Completed)
	headDone := set(h.Completed)
	h.NewlyCompleted = minus(h.Completed, baseDone)
	baseCurrent := set(b.Current)
	for _, c := range h.Current {
		if !baseCurrent[c] && !baseDone[c] {
			h.NewlyStarted = append(h.NewlyStarted, c)
		}
	}
	// Regressions: items that were done at base but are no longer done at head —
	// the most review-worthy movement, and otherwise invisible (they stay in
	// both sets).
	h.Regressed = minusSet(baseDone, headDone)
	h.AddedItems = minusSet(trackAll(*h), trackAll(b))
	h.RemovedItems = minusSet(trackAll(b), trackAll(*h))
}

func trackAll(t model.PlanTrack) map[string]bool {
	m := map[string]bool{}
	for _, ss := range [][]string{t.Completed, t.Current, t.Remaining} {
		for _, s := range ss {
			if s != "" {
				m[s] = true
			}
		}
	}
	return m
}

// withTrackRollup recomputes the flat movement fields as the union over the
// shown tracks, so a caller that only reads PlanPosition still sees this PR's
// movement and nothing else. A plan the PR did not touch contributes nothing
// even under ScopeAll — the position is repo-wide there, but the DELTA never is.
func withTrackRollup(h model.PlanPosition, b model.PlanPosition) model.PlanPosition {
	if len(h.Tracks) == 0 {
		return diffPlans(b, h) // plan.yml shape: no tracks to roll up
	}
	h.NewlyCompleted, h.NewlyStarted = nil, nil
	h.AddedItems, h.RemovedItems, h.Regressed = nil, nil, nil
	for _, t := range h.Tracks {
		h.NewlyCompleted = append(h.NewlyCompleted, t.NewlyCompleted...)
		h.NewlyStarted = append(h.NewlyStarted, t.NewlyStarted...)
		h.AddedItems = append(h.AddedItems, t.AddedItems...)
		h.RemovedItems = append(h.RemovedItems, t.RemovedItems...)
		h.Regressed = append(h.Regressed, t.Regressed...)
	}
	return h
}

// diffPlans annotates head with what moved relative to base (pure; testable).
// This is the flat, single-plan path — the plan.yml stand-in.
func diffPlans(b, h model.PlanPosition) model.PlanPosition {
	if !b.Present {
		return h // nothing to diff against (plan introduced in this PR)
	}
	baseDone := set(b.Completed)
	headDone := set(h.Completed)
	h.NewlyCompleted = minus(h.Completed, baseDone)
	if h.Current != "" && h.Current != b.Current && !baseDone[h.Current] {
		h.NewlyStarted = []string{h.Current}
	}
	h.Regressed = minusSet(baseDone, headDone)
	headAll := set(append(append(append([]string{}, h.Completed...), h.Current), h.Remaining...))
	baseAll := set(append(append(append([]string{}, b.Completed...), b.Current), b.Remaining...))
	h.AddedItems = minusSet(headAll, baseAll)
	h.RemovedItems = minusSet(baseAll, headAll)
	return h
}

func set(ss []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range ss {
		if s != "" {
			m[s] = true
		}
	}
	return m
}

func minus(ss []string, exclude map[string]bool) []string {
	var out []string
	for _, s := range ss {
		if s != "" && !exclude[s] {
			out = append(out, s)
		}
	}
	return out
}

func minusSet(a, b map[string]bool) []string {
	var out []string
	for s := range a {
		if !b[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Plan reads the plan-position delta at the reviewed ref (like every other delta):
// a live Beads store if present, else the committed .nugit/plan.yml stand-in, else
// absent. Reading at ref (not the working tree) keeps the report a pure function
// of (base, head).
func Plan(repo gitutil.Repo, ref, prefix string) model.PlanPosition {
	if pos, ok := beads.PlanPosition(repo, ref, prefix); ok {
		return pos
	}
	return planFile_(repo, ref, prefix)
}

// planFile_ reads only the committed plan.yml stand-in.
func planFile_(repo gitutil.Repo, ref, prefix string) model.PlanPosition {
	for _, name := range []string{".nugit/plan.yml", ".nugit/plan.yaml"} {
		if src, err := repo.ShowFile(ref, prefix+name); err == nil && src != "" {
			if pos, ok := parsePlan([]byte(src)); ok {
				return pos
			}
		}
	}
	return model.PlanPosition{Present: false, Note: "no Beads store or .nugit/plan.yml"}
}

// planFile is the minimal committed plan schema (a stand-in until Beads lands).
type planFile struct {
	Completed []string `yaml:"completed"`
	Current   string   `yaml:"current"`
	Remaining []string `yaml:"remaining"`
	Note      string   `yaml:"note"`
}

func parsePlan(b []byte) (model.PlanPosition, bool) {
	var pf planFile
	if err := yaml.Unmarshal(b, &pf); err != nil {
		return model.PlanPosition{}, false
	}
	return model.PlanPosition{
		Present:   true,
		Completed: pf.Completed,
		Current:   pf.Current,
		Remaining: pf.Remaining,
		Note:      pf.Note,
	}, true
}

// PlanFindings lints the committed plan store at head and adds the one check
// that needs the rest of the PR: work that changed code without moving the plan.
//
// Absent store ⇒ no findings at all. A repo that does not keep a plan store is
// not doing anything wrong, and nugit must never nag a repo into adopting a
// tool it has not chosen (the same rule the peer/contract paths follow).
func PlanFindings(repo gitutil.Repo, head, prefix string, plan model.PlanPosition, code model.CodeDelta, fail bool) []model.Finding {
	st, ok := beads.Read(repo, head, prefix)
	if !ok {
		return nil
	}
	fs := beads.Lint(st)
	if !fail {
		// Ramp in at warn (plan.mode). The store predates the check on every
		// repo that upgrades into it, so the first render after an upgrade must
		// report the defect, not gate on it.
		for i := range fs {
			if fs[i].Severity == model.SevFail {
				fs[i].Severity = model.SevWarn
			}
		}
	}
	if plan.Changed() {
		return fs
	}
	// Only code counts. A PR that is only docs, only knowledge, or only the
	// store itself is not work a plan step was supposed to be tracking, and
	// warning on those is how a useful check becomes one people mute.
	if !touchesCode(code, prefix) {
		return fs
	}
	return append(fs, model.Finding{
		Check: beads.CheckName, Severity: model.SevWarn,
		Title: "this PR changes code but moves no plan step",
		Detail: "The repo keeps a plan store and this change does not advance it. If it finishes a step, close that step in this same diff (`bd close <id>` + export) so the PR records what it completed; if it genuinely belongs to no plan, that is worth saying in the PR body. " +
			"A step closed in a later PR is a step nobody can tell was ever finished.",
	})
}

// touchesCode reports whether the PR changed anything outside the paths that
// are, by construction, not plan work: the plan store, the knowledge store, and
// prose.
func touchesCode(code model.CodeDelta, prefix string) bool {
	for _, f := range code.Files {
		p := strings.TrimPrefix(f.Path, prefix)
		if strings.HasPrefix(p, ".beads/") || strings.HasPrefix(p, ".nugit/") ||
			strings.HasPrefix(p, "docs/") || strings.HasSuffix(p, ".md") {
			continue
		}
		return true
	}
	return false
}
