package consistency

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// staleObj builds a superseded, path-bound knowledge object (ADR-0020).
func staleObj(id string, globs ...string) model.KnowledgeObject {
	o := model.KnowledgeObject{FrontMatter: model.FrontMatter{
		ID: id, Type: model.KindDecision, Scope: "global", Status: model.StatusAccepted,
		AppliesToPaths: globs,
	}}
	o.Path = ".nugit/decisions/" + strings.ToLower(id) + ".md"
	o.EffectiveStatus = model.StatusSuperseded
	return o
}

// A PR touching a file matched by a stale object's applies_to_paths counts as
// touching its governed surface — no component binding needed (ADR-0020).
func TestStaleKnowledgePathBoundFires(t *testing.T) {
	in := Input{
		AllObjects: []model.KnowledgeObject{staleObj("ADR-OLD", "third_party/**")},
		Code: model.CodeDelta{Files: []model.FileChange{
			{Path: "third_party/versions.env", Component: ""}, // unmapped, as in the pilot
		}},
	}
	fs := checkStaleKnowledge(in)
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
	if fs[0].Title != "touches third_party/versions.env, governed by superseded ADR-OLD" {
		t.Errorf("title = %q", fs[0].Title)
	}
	if !strings.Contains(fs[0].Detail, "applies_to_paths") {
		t.Errorf("detail must name the binding mechanism: %q", fs[0].Detail)
	}
	if fs[0].Severity != model.SevWarn || fs[0].Check != "stale-knowledge" {
		t.Errorf("finding shape wrong: %+v", fs[0])
	}
}

func TestStaleKnowledgePathBoundSilentCases(t *testing.T) {
	stale := staleObj("ADR-OLD", "third_party/**")

	// Non-matching path: silent.
	in := Input{
		AllObjects: []model.KnowledgeObject{stale},
		Code:       model.CodeDelta{Files: []model.FileChange{{Path: "helm/values.yaml"}}},
	}
	if fs := checkStaleKnowledge(in); len(fs) != 0 {
		t.Errorf("non-matching path must stay silent, got %+v", fs)
	}

	// The PR updates the stale object itself — the asked-for remediation must
	// not re-trigger the warning.
	obj := stale
	in = Input{
		AllObjects: []model.KnowledgeObject{stale},
		Code:       model.CodeDelta{Files: []model.FileChange{{Path: "third_party/versions.env"}}},
		Knowledge:  model.KnowledgeDelta{Changes: []model.KnowledgeChange{{Object: &obj, Status: "M"}}},
	}
	if fs := checkStaleKnowledge(in); len(fs) != 0 {
		t.Errorf("updating the object must silence the warning, got %+v", fs)
	}

	// A LIVE path-bound object never warns.
	live := staleObj("ADR-LIVE", "third_party/**")
	live.EffectiveStatus = model.StatusAccepted
	in = Input{
		AllObjects: []model.KnowledgeObject{live},
		Code:       model.CodeDelta{Files: []model.FileChange{{Path: "third_party/versions.env"}}},
	}
	if fs := checkStaleKnowledge(in); len(fs) != 0 {
		t.Errorf("a live object must not warn, got %+v", fs)
	}
}

// One finding per stale object even when several changed files match; the
// first path in sorted order wins the title.
func TestStaleKnowledgePathBoundDeduped(t *testing.T) {
	in := Input{
		AllObjects: []model.KnowledgeObject{staleObj("ADR-OLD", "k8s/**", "third_party/**")},
		Code: model.CodeDelta{Files: []model.FileChange{
			{Path: "third_party/versions.env"},
			{Path: "k8s/registry-local/configmap.yaml"},
		}},
	}
	fs := checkStaleKnowledge(in)
	if len(fs) != 1 {
		t.Fatalf("want 1 deduped finding, got %+v", fs)
	}
	if fs[0].Title != "touches k8s/registry-local/configmap.yaml, governed by superseded ADR-OLD" {
		t.Errorf("first sorted path must win the title, got %q", fs[0].Title)
	}
}

// Invalid applies_to_paths globs surface as model-health warnings — the
// mapping.InvalidPatterns discipline: report, never silently drop (ADR-0020).
func TestModelHealthInvalidAppliesGlob(t *testing.T) {
	bad := model.KnowledgeObject{FrontMatter: model.FrontMatter{
		ID: "ADR-BAD", Type: model.KindDecision, AppliesToPaths: []string{"third_party/[bad"},
	}}
	bad.Path = ".nugit/decisions/bad.md"
	in := healthInput(model.Model{})
	in.AllObjects = []model.KnowledgeObject{bad}
	fs := checkModelHealth(in)
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", titles(fs))
	}
	if fs[0].Title != `knowledge object "ADR-BAD" has an invalid applies_to_paths glob "third_party/[bad"` {
		t.Errorf("title = %q", fs[0].Title)
	}
	if fs[0].Severity != model.SevWarn || !strings.Contains(fs[0].Detail, ".nugit/decisions/bad.md") {
		t.Errorf("finding shape wrong: %+v", fs[0])
	}
}
