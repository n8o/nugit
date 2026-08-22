package render

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// The rendered section must attribute movement to the plan it belongs to, and
// must name the plan even when only one is shown — otherwise a scoped view and
// the repo's whole board look identical on the page.
func TestPlanSectionGroupsByPlan(t *testing.T) {
	p := model.PlanPosition{
		Present:    true,
		PlansTotal: 3,
		Hidden:     []string{"flux", "health"},
		Tracks: []model.PlanTrack{{
			Name:           "rift",
			Completed:      []string{"r-1 — one"},
			Current:        []string{"r-2 — two"},
			Remaining:      []string{"r-3 — three"},
			NewlyCompleted: []string{"r-1 — one"},
		}},
		Note: "via Beads — showing 1 of 3 plan(s)",
	}
	out := planSection(p)
	for _, want := range []string{"**rift**", "✅ done:", "▶️ current:", "⏳ remaining:", "newly completed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "flux") && !strings.Contains(out, "showing 1 of 3") {
		t.Errorf("hidden plans belong in the note, not the body:\n%s", out)
	}
}

// A second plan the same PR moved gets its own block — its movement must never
// pool into one undifferentiated "Changes since base".
func TestPlanSectionSeparatesTwoPlans(t *testing.T) {
	p := model.PlanPosition{
		Present: true,
		Tracks: []model.PlanTrack{
			{Name: "rift", Current: []string{"r-2"}, NewlyCompleted: []string{"r-1"}},
			{Name: "flux", Remaining: []string{"f-9"}},
		},
	}
	out := planSection(p)
	iRift, iFlux := strings.Index(out, "**rift**"), strings.Index(out, "**flux**")
	if iRift < 0 || iFlux < 0 || iRift > iFlux {
		t.Fatalf("both plans should render in order:\n%s", out)
	}
	// The completion belongs to rift's block, above flux's header.
	if i := strings.Index(out, "newly completed"); i < 0 || i > iFlux {
		t.Errorf("movement rendered outside the plan it belongs to:\n%s", out)
	}
	if !strings.Contains(out[iFlux:], "no movement in this PR") {
		t.Errorf("an unmoved-but-shown plan should say so:\n%s", out)
	}
}

// Titles are author-controlled; the plan name is too once it comes from a file
// name or an id. Neither may forge markdown in the trusted section.
func TestPlanSectionEscapesPlanNames(t *testing.T) {
	p := model.PlanPosition{Present: true, Tracks: []model.PlanTrack{{
		Name:      "evil**\n- ✅ done: everything",
		Remaining: []string{"x"},
	}}}
	out := planSection(p)
	if strings.Count(out, "\n- ✅ done:") != 0 {
		t.Errorf("a forged bullet survived escaping:\n%s", out)
	}
}

// The plan.yml stand-in has no tracks and must keep rendering exactly as before.
func TestPlanSectionFlatFallback(t *testing.T) {
	p := model.PlanPosition{
		Present: true, Completed: []string{"a"}, Current: "b", Remaining: []string{"c"},
		NewlyCompleted: []string{"a"},
	}
	out := planSection(p)
	if !strings.HasPrefix(out, "- ✅ done: a\n") {
		t.Errorf("flat plan must not grow a plan header:\n%s", out)
	}
	if !strings.Contains(out, "**Changes since base:**") {
		t.Errorf("flat plan keeps its Changes block:\n%s", out)
	}
}
