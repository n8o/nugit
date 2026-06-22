package obsidian

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestIndexLinksByFileGroupedByType(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "ADR-0011", Type: model.KindDecision}, Path: ".nugit/decisions/0011-foo.md", Body: "# ADR-0011 — External integration\n\nbody"},
		{FrontMatter: model.FrontMatter{ID: "SPEC-002", Type: model.KindSpec}, Path: ".nugit/specs/SPEC-002-contract.md", Body: "# SPEC-002 — Contract\n"},
	}
	out := Index(objs)

	// grouped headings
	if !strings.Contains(out, "## Specs") || !strings.Contains(out, "## Decisions") {
		t.Errorf("missing type groups:\n%s", out)
	}
	// link by NOTE NAME (basename, no extension), with id — title display
	if !strings.Contains(out, "[[0011-foo|ADR-0011 — External integration]]") {
		t.Errorf("decision link wrong:\n%s", out)
	}
	if !strings.Contains(out, "[[SPEC-002-contract|SPEC-002 — Contract]]") {
		t.Errorf("spec link wrong:\n%s", out)
	}
}

// The "# ADR-1 — title" heading must not produce "ADR-1 — ADR-1 — title".
func TestLabelStripsRedundantIDPrefix(t *testing.T) {
	o := model.KnowledgeObject{FrontMatter: model.FrontMatter{ID: "ADR-1"}, Body: "# ADR-1 — A title\n"}
	if got := label(o); got != "ADR-1 — A title" {
		t.Errorf("label = %q, want %q", got, "ADR-1 — A title")
	}
}
