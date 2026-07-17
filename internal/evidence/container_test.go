package evidence

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// Two-level binding (ADR-0017): a knowledge object governing a CONTAINER is
// path-bound when the container has its own paths or at least one path-bound
// child component.
func TestTierOfContainerBinding(t *testing.T) {
	leveled := model.Model{
		Components: []model.Component{
			{ID: "x1", Paths: []string{"x1/**"}, Container: "X"},
			{ID: "w1", Container: "W"}, // unbound child
		},
		Containers: []model.Container{
			{ID: "X"},                              // bound via child x1
			{ID: "Y", Paths: []string{"ycore/**"}}, // bound via own paths
			{ID: "W"},                              // no own paths, no bound child
		},
	}
	full := Signals{Model: leveled, Enforce: true, Backend: true}

	cases := []struct {
		name  string
		scope string
		want  model.Evidence
	}{
		{"container bound via child", "X", model.EvidenceEnforced},
		{"container bound via own paths", "Y", model.EvidenceEnforced},
		{"container with nothing bound", "W", model.EvidenceDeclared},
		{"unknown element still declared", "ghost", model.EvidenceDeclared},
	}
	for _, c := range cases {
		o := model.KnowledgeObject{FrontMatter: model.FrontMatter{
			ID: "K", Type: model.KindDecision, Status: model.StatusAccepted, Scope: c.scope,
		}}
		o.EffectiveStatus = model.StatusAccepted
		if got := TierOf(o, full); got != c.want {
			t.Errorf("%s: TierOf = %s, want %s", c.name, got, c.want)
		}
	}

	// Flat behavior unchanged: a paths-less component stays declared.
	flat := Signals{Model: model.Model{Components: []model.Component{{ID: "a"}}}, Enforce: true, Backend: true}
	o := model.KnowledgeObject{FrontMatter: model.FrontMatter{
		ID: "K", Type: model.KindDecision, Status: model.StatusAccepted, Scope: "a",
	}}
	o.EffectiveStatus = model.StatusAccepted
	if got := TierOf(o, flat); got != model.EvidenceDeclared {
		t.Errorf("flat unbound component: TierOf = %s, want declared", got)
	}
}
