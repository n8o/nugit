package delta

import (
	"reflect"
	"sort"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestDiffPlans(t *testing.T) {
	base := model.PlanPosition{
		Present:   true,
		Completed: []string{"e1"},
		Current:   "e2",
		Remaining: []string{"e3", "e4"},
	}
	head := model.PlanPosition{
		Present:   true,
		Completed: []string{"e1", "e2"}, // e2 finished
		Current:   "e3",                 // e3 started
		Remaining: []string{"e4", "e5"}, // e5 added; e4 still remaining
	}
	got := diffPlans(base, head)

	if !reflect.DeepEqual(got.NewlyCompleted, []string{"e2"}) {
		t.Errorf("NewlyCompleted = %v, want [e2]", got.NewlyCompleted)
	}
	if !reflect.DeepEqual(got.NewlyStarted, []string{"e3"}) {
		t.Errorf("NewlyStarted = %v, want [e3]", got.NewlyStarted)
	}
	if !reflect.DeepEqual(got.AddedItems, []string{"e5"}) {
		t.Errorf("AddedItems = %v, want [e5]", got.AddedItems)
	}
	if len(got.RemovedItems) != 0 {
		t.Errorf("RemovedItems = %v, want none", got.RemovedItems)
	}
	if !got.Changed() {
		t.Error("Changed() should be true")
	}
}

func TestDiffPlansNoBase(t *testing.T) {
	// plan introduced in this PR (no base plan) -> no diff annotations, no panic
	head := model.PlanPosition{Present: true, Remaining: []string{"e1"}}
	got := diffPlans(model.PlanPosition{Present: false}, head)
	if got.Changed() {
		t.Errorf("a plan with no base should report no changes, got %+v", got)
	}
}

func TestDiffPlansRemoved(t *testing.T) {
	base := model.PlanPosition{Present: true, Remaining: []string{"e1", "e2"}}
	head := model.PlanPosition{Present: true, Remaining: []string{"e1"}}
	got := diffPlans(base, head)
	r := append([]string{}, got.RemovedItems...)
	sort.Strings(r)
	if !reflect.DeepEqual(r, []string{"e2"}) {
		t.Errorf("RemovedItems = %v, want [e2]", got.RemovedItems)
	}
}
