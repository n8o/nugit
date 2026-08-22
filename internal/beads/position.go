package beads

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// Position maps a store onto a plan position, grouped by plan.
//
// When only is non-nil it selects which plans render; every other plan is
// counted in PlansTotal and named in Hidden. A nil set renders everything —
// that is the whole-repo view, not the PR view.
//
// Epic preference is deliberately a STORE-wide rule, not a per-plan one: if any
// bead anywhere is an epic, epics are the plan-step type in this repo, and a
// plan made of tasks is a plan whose steps were mistyped. Deciding per plan
// would make the same store render two different unit vocabularies depending on
// which PR was reading it.
func Position(st Store, only map[string]bool) model.PlanPosition {
	scope := st.Issues
	var epics []Issue
	for _, it := range scope {
		if it.Type == "epic" {
			epics = append(epics, it)
		}
	}
	epicsOnly := len(epics) > 0
	if epicsOnly {
		scope = epics
	}

	// Group in first-appearance order — the file's order is the plan's order.
	var order []string
	byPlan := map[string][]Issue{}
	for _, it := range scope {
		if _, ok := byPlan[it.Plan]; !ok {
			order = append(order, it.Plan)
		}
		byPlan[it.Plan] = append(byPlan[it.Plan], it)
	}

	pos := model.PlanPosition{Present: true, PlansTotal: len(order)}
	var hiddenLive []string // hidden plans with a step actually in flight
	for _, name := range order {
		if only != nil && !only[name] {
			pos.Hidden = append(pos.Hidden, name)
			for _, it := range byPlan[name] {
				if isActive(it.Status) {
					hiddenLive = append(hiddenLive, name)
					break
				}
			}
			continue
		}
		t := model.PlanTrack{Name: name}
		for _, it := range byPlan[name] {
			switch {
			case isDone(it.Status):
				t.Completed = append(t.Completed, label(it))
			case isActive(it.Status):
				t.Current = append(t.Current, label(it))
			default:
				t.Remaining = append(t.Remaining, label(it))
			}
		}
		pos.Tracks = append(pos.Tracks, t)
	}
	flatten(&pos)
	pos.Note = note(pos, st, scope, epicsOnly, only != nil, hiddenLive)
	return pos
}

// flatten fills the legacy flat fields from the shown tracks. Current keeps its
// singular shape (first in-flight step wins) because it is what the compact
// render and the JSON contract expose; PlanTrack.Current carries the rest.
func flatten(pos *model.PlanPosition) {
	pos.Completed = nil
	pos.Remaining = nil
	pos.Current = ""
	for _, t := range pos.Tracks {
		pos.Completed = append(pos.Completed, t.Completed...)
		for i, c := range t.Current {
			if pos.Current == "" && i == 0 {
				pos.Current = c
				continue
			}
			pos.Remaining = append(pos.Remaining, c)
		}
		pos.Remaining = append(pos.Remaining, t.Remaining...)
	}
}

func note(pos model.PlanPosition, st Store, scope []Issue, epicsOnly, scoped bool, hiddenLive []string) string {
	unit := "issue"
	hidden := ""
	if epicsOnly {
		unit = "epic"
		if n := len(st.Issues) - len(scope); n > 0 {
			hidden = fmt.Sprintf(", %d non-epic issue(s) not shown", n)
		}
	}
	shownSteps := len(pos.Completed) + len(pos.Remaining)
	if pos.Current != "" {
		shownSteps++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "via Beads — %d %s(s): %d done, %d remaining%s (forecast)",
		shownSteps, unit, len(pos.Completed), len(pos.Remaining), hidden)
	if len(pos.Hidden) > 0 {
		fmt.Fprintf(&b, ". Showing %d of %d plan(s) — this PR's own", len(pos.Tracks), pos.PlansTotal)
		// Name only the plans someone is actually working right now, and only a
		// few of them. A finished or not-yet-started plan cannot collide with
		// this PR, and a list of sixteen names is read as noise and skipped —
		// which would lose the two names that mattered.
		if len(hiddenLive) > 0 {
			fmt.Fprintf(&b, "; also in flight elsewhere: %s", strings.Join(trunc(hiddenLive, 4), ", "))
		}
	} else if scoped && pos.PlansTotal > 1 {
		fmt.Fprintf(&b, ". All %d plan(s) moved by this PR", pos.PlansTotal)
	}
	return b.String()
}
