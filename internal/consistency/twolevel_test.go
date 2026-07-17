package consistency

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
)

func leveledModel(rels ...[2]string) model.Model {
	m := model.Model{
		Components: []model.Component{
			{ID: "x1", Paths: []string{"x1/**"}, Container: "X"},
			{ID: "x2", Paths: []string{"x2/**"}, Container: "X"},
			{ID: "y1", Paths: []string{"y1/**"}, Container: "Y"},
			{ID: "z1", Paths: []string{"z1/**"}, Container: "Z"},
		},
		Containers: []model.Container{
			{ID: "X"}, {ID: "Y", Paths: []string{"ycore/**"}}, {ID: "Z"},
		},
	}
	for _, r := range rels {
		m.Relationships = append(m.Relationships, model.Relationship{Src: r[0], Dst: r[1]})
	}
	return m
}

func TestC4CodeDetailWording(t *testing.T) {
	m := leveledModel()
	flat := model.Model{Components: []model.Component{{ID: "a"}, {ID: "b"}}}

	// Flat pair: byte-identical to the historical wording.
	got := c4CodeDetail(flat, "a", "b", "a/a.go")
	want := "code in a now imports b but workspace.dsl has no `a -> b` relationship; " +
		"add the relationship to the model or remove the import (introduced via a/a.go)"
	if got != want {
		t.Errorf("flat wording drifted:\n got %q\nwant %q", got, want)
	}

	// Same-container pair: also the historical wording (the container-level
	// alternative would be an intra-container roll-up, which never covers).
	got = c4CodeDetail(m, "x1", "x2", "x1/x1.go")
	if strings.Contains(got, "container level") {
		t.Errorf("intra-container detail must not offer a container edge: %q", got)
	}

	// Cross-container: both levels offered, component level first.
	got = c4CodeDetail(m, "x1", "y1", "x1/x1.go")
	want = "code in x1 now imports y1 but workspace.dsl declares neither `x1 -> y1` (component level) " +
		"nor `X -> Y` (container level — covers every X → Y component dependency); " +
		"add one to the model or remove the import (introduced via x1/x1.go)"
	if got != want {
		t.Errorf("cross-container wording:\n got %q\nwant %q", got, want)
	}

	// Container-owned source file: the literal pair names the container.
	got = c4CodeDetail(m, "Y", "z1", "ycore/core.go")
	if !strings.Contains(got, "`Y -> z1` (component level)") || !strings.Contains(got, "`Y -> Z` (container level") {
		t.Errorf("container-source wording missing a level: %q", got)
	}

	// Both endpoints already containers: the two suggestions would be the same
	// edge — keep the flat wording.
	got = c4CodeDetail(m, "X", "Y", "x1/x1.go")
	if strings.Contains(got, "container level") {
		t.Errorf("container-to-container detail must not duplicate the suggestion: %q", got)
	}
}

func TestFlagDirEdgesTwoLevel(t *testing.T) {
	m := leveledModel()
	in := Input{
		HeadModel: m,
		Mapper:    mapping.New(m),
		Code: model.CodeDelta{Files: []model.FileChange{
			{Path: "x1/f.py", Status: "M", Component: "x1"},
			{Path: "ycore/g.py", Status: "M", Component: "Y"},
		}},
	}
	fs := flagDirEdges(in, "python<->code",
		"Python imports %s → %s, but workspace.dsl declares no such relationship; add it or remove the import",
		[][2]string{
			{"x1", "y1"},    // cross-container, undeclared -> fires with alternative
			{"ycore", "y1"}, // container-owned dir -> own child: lineage, silent
		})
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
	if fs[0].Title != "undeclared dependency x1 → y1" {
		t.Errorf("title = %q", fs[0].Title)
	}
	if !strings.HasSuffix(fs[0].Detail, " (container-level alternative: declare `X -> Y`)") {
		t.Errorf("container-level alternative missing: %q", fs[0].Detail)
	}

	// With the container edge declared, the same dir edge is covered.
	m2 := leveledModel([2]string{"X", "Y"})
	in.HeadModel = m2
	in.Mapper = mapping.New(m2)
	if fs := flagDirEdges(in, "python<->code", "%s %s", [][2]string{{"x1", "y1"}}); len(fs) != 0 {
		t.Errorf("declared container edge must cover the dir edge: %+v", fs)
	}

	// Flat models: wording has no container suffix.
	flat := model.Model{
		Components:    []model.Component{{ID: "a", Paths: []string{"a/**"}}, {ID: "b", Paths: []string{"b/**"}}},
		Relationships: nil,
	}
	fin := Input{
		HeadModel: flat,
		Mapper:    mapping.New(flat),
		Code:      model.CodeDelta{Files: []model.FileChange{{Path: "a/f.py", Status: "M", Component: "a"}}},
	}
	fs = flagDirEdges(fin, "python<->code",
		"Python imports %s → %s, but workspace.dsl declares no such relationship; add it or remove the import",
		[][2]string{{"a", "b"}})
	if len(fs) != 1 || strings.Contains(fs[0].Detail, "container") {
		t.Errorf("flat wording drifted: %+v", fs)
	}
}
