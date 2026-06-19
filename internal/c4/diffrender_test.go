package c4

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestMermaidDiffStyling(t *testing.T) {
	head := model.Model{
		Components: []model.Component{
			{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"},
		},
		Relationships: []model.Relationship{
			{Src: "a", Dst: "b"}, // added in this delta
			{Src: "a", Dst: "c"}, // pre-existing
		},
	}
	d := model.C4Delta{
		AddedComponents: []model.Component{{ID: "a", Name: "A"}},
		AddedRels:       []model.Relationship{{Src: "a", Dst: "b"}},
		RemovedRels:     []model.Relationship{{Src: "x", Dst: "a"}}, // x not in head
	}
	out := MermaidDiff(d, head)

	if !strings.Contains(out, "```mermaid") || !strings.Contains(out, "graph LR") {
		t.Fatalf("not a mermaid block:\n%s", out)
	}
	if !strings.Contains(out, `a["A"]:::add`) {
		t.Errorf("added component not styled :::add:\n%s", out)
	}
	if !strings.Contains(out, "a ==> b") {
		t.Errorf("added edge not bold (==>):\n%s", out)
	}
	if !strings.Contains(out, "x -.-> a") {
		t.Errorf("removed edge not dashed (-.->):\n%s", out)
	}
	if !strings.Contains(out, "classDef add") || !strings.Contains(out, "linkStyle") {
		t.Errorf("missing classDef/linkStyle:\n%s", out)
	}
	// determinism
	if MermaidDiff(d, head) != out {
		t.Error("non-deterministic output")
	}
}

// Two ids that sanitize to the same token must NOT collapse into one node.
func TestMermaidDiffIDCollision(t *testing.T) {
	head := model.Model{
		Components: []model.Component{{ID: "a.b"}, {ID: "a_b"}},
		Relationships: []model.Relationship{
			{Src: "a.b", Dst: "a_b"}, // distinct components -> must NOT be a self-loop
		},
	}
	d := model.C4Delta{AddedRels: []model.Relationship{{Src: "a.b", Dst: "a_b"}}}
	out := MermaidDiff(d, head)
	// two distinct node declarations
	if strings.Count(out, `["`) != 2 {
		t.Errorf("collision merged the two nodes:\n%s", out)
	}
	// the edge must connect two DIFFERENT node ids (no self-loop)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "==>") {
			parts := strings.Fields(line)
			if len(parts) == 3 && parts[0] == parts[2] {
				t.Errorf("edge collapsed to a self-loop: %q", line)
			}
		}
	}
}

// A label with a backslash/control char must not be double-escaped (no %q).
func TestMermaidDiffLabelNotGoQuoted(t *testing.T) {
	head := model.Model{Components: []model.Component{{ID: "a", Name: `C:\path`}}}
	d := model.C4Delta{AddedComponents: []model.Component{{ID: "a", Name: `C:\path`}}}
	out := MermaidDiff(d, head)
	if strings.Contains(out, `\\`) {
		t.Errorf("backslash double-escaped (Go %%q leaked into Mermaid):\n%s", out)
	}
}

func TestMermaidDiffEmpty(t *testing.T) {
	if MermaidDiff(model.C4Delta{}, model.Model{}) != "" {
		t.Error("empty delta must produce no diagram")
	}
}

// A change touching a hub must not explode the diagram past the focus cap.
func TestMermaidDiffFocusCap(t *testing.T) {
	var comps []model.Component
	var rels []model.Relationship
	comps = append(comps, model.Component{ID: "hub"})
	for i := 0; i < 40; i++ {
		id := "n" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		comps = append(comps, model.Component{ID: id})
		rels = append(rels, model.Relationship{Src: id, Dst: "hub"})
	}
	head := model.Model{Components: comps, Relationships: rels}
	// change the hub's description (a ChangedComponent) -> 40 one-hop neighbors
	d := model.C4Delta{ChangedComponents: []model.ComponentChange{
		{After: model.Component{ID: "hub"}, Fields: []string{"description"}},
	}}
	out := MermaidDiff(d, head)
	nodes := strings.Count(out, "[\"")
	if nodes > focusCap {
		t.Errorf("focus cap exceeded: %d nodes drawn (cap %d)\n", nodes, focusCap)
	}
}
