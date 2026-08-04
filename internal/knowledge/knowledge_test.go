package knowledge

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
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

const pathBoundDecision = `---
schema_version: 1
id: ADR-0020
type: decision
scope: global
status: accepted
created: 2026-08-01T00:00:00Z
applies_to_paths:
  - "third_party/versions.env"
  - "k8s/registry-local/**"
provenance:
  commit: abc123
---

# ADR-0020 — Pin the draft version
`

func TestAppliesToPathsParsing(t *testing.T) {
	obj, ok := ParseObject(".nugit/decisions/0020.md", pathBoundDecision)
	if !ok {
		t.Fatal("expected ok")
	}
	want := []string{"third_party/versions.env", "k8s/registry-local/**"}
	if len(obj.AppliesToPaths) != 2 || obj.AppliesToPaths[0] != want[0] || obj.AppliesToPaths[1] != want[1] {
		t.Fatalf("applies_to_paths = %v, want %v", obj.AppliesToPaths, want)
	}
	for path, want := range map[string]bool{
		"third_party/versions.env":          true,
		"./third_party/versions.env":        true, // "./" prefix normalized
		"k8s/registry-local/configmap.yaml": true,
		"k8s/other/configmap.yaml":          false,
		"third_party/other.env":             false,
	} {
		if got := AppliesTo(obj, path); got != want {
			t.Errorf("AppliesTo(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestInvalidAppliesGlobReportedNeverMatches(t *testing.T) {
	o := &model.KnowledgeObject{
		FrontMatter: model.FrontMatter{ID: "ADR-BAD", AppliesToPaths: []string{"third_party/[bad", "helm/**"}},
		Path:        ".nugit/decisions/bad.md",
	}
	// The invalid glob must never match (and never panic); the valid one still works.
	if AppliesTo(o, "third_party/[bad") {
		t.Error("an invalid glob must match nothing")
	}
	if !AppliesTo(o, "helm/values.yaml") {
		t.Error("the valid sibling glob must still match")
	}
	bad := InvalidAppliesGlobs([]model.KnowledgeObject{*o})
	if len(bad) != 1 || bad[0].ID != "ADR-BAD" || bad[0].Pattern != "third_party/[bad" ||
		bad[0].Path != ".nugit/decisions/bad.md" {
		t.Fatalf("InvalidAppliesGlobs = %+v, want the one bad glob reported (never silently dropped)", bad)
	}
	if got := InvalidAppliesGlobs(nil); len(got) != 0 {
		t.Errorf("no objects -> no reports, got %+v", got)
	}
}

func TestResolveAmendedBy(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "ADR-1", Status: model.StatusAccepted}},
		{FrontMatter: model.FrontMatter{ID: "ADR-2", Status: model.StatusAccepted,
			RelatesTo: []string{"amends:ADR-1"}}},
		{FrontMatter: model.FrontMatter{ID: "ADR-3", Status: model.StatusAccepted,
			RelatesTo: []string{"amends:ADR-1"}}},
		// A superseded amender must not annotate.
		{FrontMatter: model.FrontMatter{ID: "ADR-4", Status: model.StatusAccepted,
			RelatesTo: []string{"amends:ADR-1"}}},
		{FrontMatter: model.FrontMatter{ID: "ADR-5", Status: model.StatusAccepted,
			Supersedes: "ADR-4"}},
	}
	ResolveEffectiveStatus(objs)
	ResolveAmendedBy(objs)
	got := objs[0].AmendedBy
	if len(got) != 2 || got[0] != "ADR-2" || got[1] != "ADR-3" {
		t.Fatalf("AmendedBy = %v, want [ADR-2 ADR-3] (sorted, superseded amender excluded)", got)
	}
	if objs[0].EffectiveStatus != model.StatusAccepted {
		t.Errorf("amended object must stay live, got %s", objs[0].EffectiveStatus)
	}
	if objs[1].AmendedBy != nil {
		t.Errorf("amender itself must not be marked amended: %v", objs[1].AmendedBy)
	}
}
