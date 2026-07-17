package evidence

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func mdl(structural bool, comps ...model.Component) model.Model {
	m := model.Model{Components: comps, Properties: map[string]string{}}
	if structural {
		m.Properties["nugit_structural"] = "true"
	}
	return m
}

func comp(id string, bound bool) model.Component {
	c := model.Component{ID: id}
	if bound {
		c.Paths = []string{id + "/**"}
	}
	return c
}

func obj(status model.Status, scope string, relates ...string) model.KnowledgeObject {
	o := model.KnowledgeObject{FrontMatter: model.FrontMatter{
		ID: "X", Type: model.KindDecision, Status: status, Scope: scope, RelatesTo: relates,
	}}
	o.EffectiveStatus = status
	return o
}

func TestTierOfMatrix(t *testing.T) {
	full := Signals{Model: mdl(false, comp("a", true), comp("b", true)), Enforce: true, Backend: true}

	cases := []struct {
		name string
		o    model.KnowledgeObject
		s    Signals
		want model.Evidence
	}{
		{"scoped, bound, enforced", obj(model.StatusAccepted, "a"), full, model.EvidenceEnforced},
		{"edge-governed, bound, enforced", obj(model.StatusAccepted, "global", "constrains:a"), full, model.EvidenceEnforced},
		{"warn mode degrades to checked", obj(model.StatusAccepted, "a"),
			Signals{Model: full.Model, Enforce: false, Backend: true}, model.EvidenceChecked},
		{"no backend degrades to checked", obj(model.StatusAccepted, "a"),
			Signals{Model: full.Model, Enforce: true, Backend: false}, model.EvidenceChecked},
		{"structural model degrades to checked", obj(model.StatusAccepted, "a"),
			Signals{Model: mdl(true, comp("a", true)), Enforce: true, Backend: true}, model.EvidenceChecked},
		{"partially bound is checked", obj(model.StatusAccepted, "a", "constrains:unbound"),
			Signals{Model: mdl(false, comp("a", true), comp("unbound", false)), Enforce: true, Backend: true},
			model.EvidenceChecked},
		{"global with no edges is declared", obj(model.StatusAccepted, "global"), full, model.EvidenceDeclared},
		{"governs only unknown components is declared", obj(model.StatusAccepted, "global", "constrains:ghost"),
			full, model.EvidenceDeclared},
		{"proposed beats enforced", obj(model.StatusProposed, "a"), full, model.EvidenceProposed},
		{"stale beats proposed", func() model.KnowledgeObject {
			o := obj(model.StatusProposed, "a")
			o.EffectiveStatus = model.StatusSuperseded
			return o
		}(), full, model.EvidenceStale},
		{"invalidated is stale", func() model.KnowledgeObject {
			o := obj(model.StatusAccepted, "a")
			o.EffectiveStatus = model.StatusInvalidated
			return o
		}(), full, model.EvidenceStale},
	}
	for _, tc := range cases {
		if got := TierOf(tc.o, tc.s); got != tc.want {
			t.Errorf("%s: want %s, got %s", tc.name, tc.want, got)
		}
	}
}

func TestAnnotateSkipsUntyped(t *testing.T) {
	objs := []model.KnowledgeObject{
		{}, // untyped (no id) — must stay un-annotated
		obj(model.StatusAccepted, "a"),
	}
	Annotate(objs, Signals{Model: mdl(false, comp("a", true)), Enforce: true, Backend: true})
	if objs[0].Evidence != "" {
		t.Errorf("untyped object must stay un-annotated, got %q", objs[0].Evidence)
	}
	if objs[1].Evidence != model.EvidenceEnforced {
		t.Errorf("typed object must be annotated, got %q", objs[1].Evidence)
	}
}

func TestAnnotateDeltaPrefersResolvedSet(t *testing.T) {
	s := Signals{Model: mdl(false, comp("a", true)), Enforce: true, Backend: true}
	// The delta's standalone parse thinks the object is accepted; the resolved
	// head set knows it is superseded.
	dobj := obj(model.StatusAccepted, "a")
	d := model.KnowledgeDelta{Changes: []model.KnowledgeChange{{Object: &dobj}}}
	AnnotateDelta(&d, map[string]model.Evidence{"X": model.EvidenceStale}, s)
	if d.Changes[0].Object.Evidence != model.EvidenceStale {
		t.Errorf("delta must copy the resolved tier, got %q", d.Changes[0].Object.Evidence)
	}
	// Absent from the head set (deleted at head): standalone fallback.
	dobj2 := obj(model.StatusAccepted, "a")
	dobj2.ID = "GONE"
	d2 := model.KnowledgeDelta{Changes: []model.KnowledgeChange{{Object: &dobj2}}}
	AnnotateDelta(&d2, map[string]model.Evidence{}, s)
	if d2.Changes[0].Object.Evidence != model.EvidenceEnforced {
		t.Errorf("deleted-at-head object must fall back to standalone derivation, got %q", d2.Changes[0].Object.Evidence)
	}
}
