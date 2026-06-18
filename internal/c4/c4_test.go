package c4

import "testing"

const sample = `workspace "x" "desc" {
  model {
    sys = softwareSystem "S" {
      a = component "A" {
        properties { paths "a/**" }
      }
      b = component "B" "the b" "Go" {
        properties { paths "b/**,c/**" }
      }
      a -> b "uses"
    }
  }
  views { component sys { include * } theme default }
}`

func TestParse(t *testing.T) {
	m := Parse(sample)
	if len(m.Components) != 2 {
		t.Fatalf("want 2 components, got %d: %+v", len(m.Components), m.Components)
	}
	a, ok := m.Comp("a")
	if !ok || len(a.Paths) != 1 || a.Paths[0] != "a/**" {
		t.Errorf("component a paths wrong: %+v", a)
	}
	b, _ := m.Comp("b")
	if b.Tech != "Go" {
		t.Errorf("component b tech = %q, want Go", b.Tech)
	}
	if len(b.Paths) != 2 || b.Paths[0] != "b/**" || b.Paths[1] != "c/**" {
		t.Errorf("component b paths = %+v, want [b/** c/**]", b.Paths)
	}
	if !m.HasRelationship("a", "b") {
		t.Errorf("expected relationship a->b; rels=%+v", m.Relationships)
	}
	// the views block must not leak phantom components/relationships
	if len(m.Relationships) != 1 {
		t.Errorf("want exactly 1 relationship, got %+v", m.Relationships)
	}
}

func TestDiff(t *testing.T) {
	base := Parse(`workspace x { model { s = softwareSystem S {
		a = component A { properties { paths "a/**" } }
		b = component B { properties { paths "b/**" } }
		a -> b "old"
	} } }`)
	head := Parse(`workspace x { model { s = softwareSystem S {
		a = component A { properties { paths "a/**" } }
		b = component B { properties { paths "b/**" } }
		c = component C { properties { paths "c/**" } }
		a -> c "new"
	} } }`)
	d := Diff(base, head, "")
	if len(d.AddedComponents) != 1 || d.AddedComponents[0].ID != "c" {
		t.Errorf("want added component c, got %+v", d.AddedComponents)
	}
	if len(d.AddedRels) != 1 || d.AddedRels[0].Dst != "c" {
		t.Errorf("want added rel a->c, got %+v", d.AddedRels)
	}
	if len(d.RemovedRels) != 1 || d.RemovedRels[0].Dst != "b" {
		t.Errorf("want removed rel a->b, got %+v", d.RemovedRels)
	}
	if d.Empty() {
		t.Error("delta should not be empty")
	}
}
