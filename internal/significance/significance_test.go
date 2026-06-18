package significance

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestClassify(t *testing.T) {
	c4Changed := model.C4Delta{AddedComponents: []model.Component{{ID: "x"}}}
	empty := model.C4Delta{}

	twoComp := model.CodeDelta{Files: []model.FileChange{
		{Component: "a"}, {Component: "b"},
	}}
	oneSmall := model.CodeDelta{
		Files:    []model.FileChange{{Component: "a", Added: 1, Deleted: 1}},
		TotalAdd: 1, TotalDel: 1,
	}
	noKnow := model.KnowledgeDelta{}

	if got := Classify(c4Changed, oneSmall, noKnow, false, Options{}).Tier; got != model.TierArchitectural {
		t.Errorf("model change → %s, want architectural", got)
	}
	if got := Classify(empty, oneSmall, noKnow, true, Options{}).Tier; got != model.TierArchitectural {
		t.Errorf("undeclared cross edge → %s, want architectural", got)
	}
	if got := Classify(empty, twoComp, noKnow, false, Options{}).Tier; got != model.TierFeature {
		t.Errorf("multi-component → %s, want feature", got)
	}
	if got := Classify(empty, oneSmall, noKnow, false, Options{}).Tier; got != model.TierTrivial {
		t.Errorf("small single-component → %s, want trivial", got)
	}
}

func TestReasonsAlwaysPresent(t *testing.T) {
	s := Classify(model.C4Delta{}, model.CodeDelta{Files: []model.FileChange{{Component: "a"}}}, model.KnowledgeDelta{}, false, Options{})
	if len(s.Reasons) == 0 {
		t.Error("classifier must always explain itself")
	}
}
