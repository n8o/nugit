package c4

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// A label with quotes/brackets must be entity-escaped, not Go-quoted, or it
// breaks GitHub's Mermaid parser (P5 review finding).
func TestMermaidEscaping(t *testing.T) {
	m := model.Model{
		Components: []model.Component{{ID: "ui", Name: `Say "hi" [UI] (x)`}},
	}
	out := Mermaid(m)
	if strings.Contains(out, `"hi"`) || strings.Contains(out, "[UI]") {
		t.Errorf("raw quotes/brackets leaked into Mermaid: %q", out)
	}
	if !strings.Contains(out, "&quot;") || !strings.Contains(out, "&#91;") {
		t.Errorf("label not entity-escaped: %q", out)
	}
	if !strings.HasPrefix(out, "graph LR\n") {
		t.Errorf("missing header: %q", out)
	}
}

func TestMermaidDeterministicEdges(t *testing.T) {
	m := model.Model{
		Components:    []model.Component{{ID: "b"}, {ID: "a"}, {ID: "c"}},
		Relationships: []model.Relationship{{Src: "b", Dst: "c"}, {Src: "a", Dst: "b"}},
	}
	if Mermaid(m) != Mermaid(m) {
		t.Fatal("non-deterministic output")
	}
	out := Mermaid(m)
	// components sorted: a before b before c
	if strings.Index(out, "a[") > strings.Index(out, "b[") {
		t.Errorf("components not sorted: %q", out)
	}
}
