package consistency

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
)

func healthInput(m model.Model) Input {
	return Input{HeadModel: m, Mapper: mapping.New(m)}
}

func titles(fs []model.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Title)
	}
	return out
}

// Components and containers share one id namespace: a container reusing a
// component id is a duplicate (last-wins collapse in lookups and the mapper).
func TestModelHealthDuplicateAcrossNamespaces(t *testing.T) {
	m := model.Model{
		Components: []model.Component{{ID: "svc"}, {ID: "ok"}},
		Containers: []model.Container{{ID: "svc"}},
	}
	fs := checkModelHealth(healthInput(m))
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
	want := `duplicate element id "svc" (2 declarations)`
	if fs[0].Title != want {
		t.Errorf("title = %q, want %q", fs[0].Title, want)
	}
}

// A component-only duplicate keeps today's wording byte-for-byte.
func TestModelHealthComponentDuplicateWordingUnchanged(t *testing.T) {
	m := model.Model{Components: []model.Component{{ID: "a"}, {ID: "a"}}}
	fs := checkModelHealth(healthInput(m))
	if len(fs) != 1 || fs[0].Title != `duplicate component id "a" (2 declarations)` {
		t.Errorf("component duplicate wording drifted: %+v", fs)
	}
	if fs[0].Detail != "workspace.dsl declares this id more than once; only the last binding is used — rename or merge them" {
		t.Errorf("component duplicate detail drifted: %q", fs[0].Detail)
	}
}

func TestModelHealthInvalidGlobKinds(t *testing.T) {
	m := model.Model{
		Components: []model.Component{{ID: "a", Paths: []string{"a/[bad"}}},
		Containers: []model.Container{{ID: "X", Paths: []string{"x/[bad"}}},
	}
	fs := checkModelHealth(healthInput(m))
	if len(fs) != 2 {
		t.Fatalf("want 2 findings, got %+v", titles(fs))
	}
	if fs[0].Title != `container "X" has an invalid path glob "x/[bad"` {
		t.Errorf("container glob title = %q", fs[0].Title)
	}
	// Component wording must stay byte-identical to the pre-container output.
	if fs[1].Title != `component "a" has an invalid path glob "a/[bad"` {
		t.Errorf("component glob title drifted: %q", fs[1].Title)
	}
	if fs[1].Detail != "this glob is syntactically invalid and matches no files, so the component owns nothing" {
		t.Errorf("component glob detail drifted: %q", fs[1].Detail)
	}
}

func TestModelHealthUnknownRelationshipEndpoint(t *testing.T) {
	m := model.Model{
		Components: []model.Component{{ID: "a", Container: "X"}},
		Containers: []model.Container{{ID: "X"}},
		Relationships: []model.Relationship{
			{Src: "a", Dst: "ghost"}, // unknown dst
			{Src: "a", Dst: "X"},     // component -> container: both known
			{Src: "X", Dst: "a"},     // container -> component: both known
		},
	}
	fs := checkModelHealth(healthInput(m))
	if len(fs) != 1 {
		t.Fatalf("want exactly the unknown-endpoint finding, got %+v", titles(fs))
	}
	if fs[0].Severity != model.SevWarn ||
		fs[0].Title != `relationship endpoint "ghost" matches no component or container` {
		t.Errorf("unknown endpoint finding wrong: %+v", fs[0])
	}
}

// The unknown-endpoint warning must stay silent on a healthy two-level model
// (all endpoints are component or container ids).
func TestModelHealthSilentOnLeveledModel(t *testing.T) {
	m := model.Model{
		Components: []model.Component{
			{ID: "x1", Container: "X", Paths: []string{"x1/**"}},
			{ID: "y1", Container: "Y", Paths: []string{"y1/**"}},
		},
		Containers: []model.Container{{ID: "X"}, {ID: "Y", Paths: []string{"ycore/**"}}},
		Relationships: []model.Relationship{
			{Src: "x1", Dst: "y1"}, {Src: "X", Dst: "Y"}, {Src: "x1", Dst: "Y"},
		},
	}
	if fs := checkModelHealth(healthInput(m)); len(fs) != 0 {
		t.Errorf("healthy leveled model produced findings: %v", strings.Join(titles(fs), "; "))
	}
}
