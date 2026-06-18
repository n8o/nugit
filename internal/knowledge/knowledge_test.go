package knowledge

import (
	"testing"

	"github.com/burrowfarm/nugit/internal/model"
)

const decision = `---
schema_version: 1
id: ADR-0009
type: decision
scope: render
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:render
provenance:
  commit: abc123
---

# ADR-0009 — Something

## Rejected

- The bad option, because reasons.
`

func TestParseObject(t *testing.T) {
	obj, ok := ParseObject(".nugit/decisions/0009.md", decision)
	if !ok {
		t.Fatal("expected ok")
	}
	if obj.ID != "ADR-0009" || obj.Type != model.KindDecision || obj.Scope != "render" {
		t.Errorf("front-matter parse wrong: %+v", obj.FrontMatter)
	}
	if obj.SchemaVersion != 1 {
		t.Errorf("schema_version = %d", obj.SchemaVersion)
	}
	if len(obj.RelatesTo) != 1 || obj.RelatesTo[0] != "constrains:render" {
		t.Errorf("relates_to = %+v", obj.RelatesTo)
	}
	if rej := RejectedSection(obj.Body); rej == "" {
		t.Error("expected a Rejected section")
	}
}

func TestNoFrontMatter(t *testing.T) {
	if _, ok := ParseObject("x.md", "# just markdown\n\nno front matter"); ok {
		t.Error("expected ok=false for file with no front-matter")
	}
}

func TestEffectiveStatusDerived(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "ADR-1", Status: model.StatusAccepted}},
		{FrontMatter: model.FrontMatter{ID: "ADR-2", Status: model.StatusAccepted, Supersedes: "ADR-1"}},
	}
	ResolveEffectiveStatus(objs)
	if objs[0].EffectiveStatus != model.StatusSuperseded {
		t.Errorf("ADR-1 effective status = %q, want superseded (derived from graph)", objs[0].EffectiveStatus)
	}
	if objs[1].EffectiveStatus != model.StatusAccepted {
		t.Errorf("ADR-2 effective status = %q, want accepted", objs[1].EffectiveStatus)
	}
}

func TestParseEdge(t *testing.T) {
	e := ParseEdge("constrains:render")
	if e.Relation != "constrains" || e.Target != "render" {
		t.Errorf("ParseEdge = %+v", e)
	}
	if ParseEdge("render").Relation != "" {
		t.Error("bare target should have empty relation")
	}
}
