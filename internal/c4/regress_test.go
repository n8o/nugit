package c4

import "testing"

// Regression: arrows and element refs inside a views block must not leak into
// the model as phantom relationships/components.
func TestViewsBlockDoesNotLeak(t *testing.T) {
	src := `workspace "x" {
	  model {
	    s = softwareSystem "S" {
	      a = component "A" { properties { paths "a/**" } }
	      b = component "B" { properties { paths "b/**" } }
	      a -> b "real edge"
	    }
	  }
	  views {
	    dynamic * "Flow" {
	      b -> a "response"
	    }
	    z = component s "phantom" { include * }
	    styles { element "Element" { background "#fff" } }
	  }
	}`
	m := Parse(src)
	if len(m.Components) != 2 {
		t.Fatalf("views leaked components: want 2, got %d (%+v)", len(m.Components), m.Components)
	}
	if len(m.Relationships) != 1 {
		t.Fatalf("views leaked relationships: %+v", m.Relationships)
	}
	if !m.HasRelationship("a", "b") {
		t.Error("real edge a->b missing")
	}
	if m.HasRelationship("b", "a") {
		t.Error("phantom edge b->a leaked from a dynamic view")
	}
}

// Regression: a relationship description change is a real, visible delta.
func TestRelDescChangeDetected(t *testing.T) {
	base := Parse(`workspace x { model { s = softwareSystem S {
		a = component "A" {} b = component "B" {} a -> b "old" } } }`)
	head := Parse(`workspace x { model { s = softwareSystem S {
		a = component "A" {} b = component "B" {} a -> b "new" } } }`)
	d := Diff(base, head, "")
	if len(d.ChangedRels) != 1 {
		t.Fatalf("want 1 changed rel, got %+v", d.ChangedRels)
	}
	if d.ChangedRels[0].Before.Desc != "old" || d.ChangedRels[0].After.Desc != "new" {
		t.Errorf("bad change record: %+v", d.ChangedRels[0])
	}
	if len(d.AddedRels) != 0 || len(d.RemovedRels) != 0 {
		t.Error("a description change must not appear as add/remove")
	}
	if d.Empty() {
		t.Error("delta with a changed rel must not be Empty()")
	}
}

// Regression: positional 4th-arg tags are captured.
func TestPositionalTags(t *testing.T) {
	m := Parse(`workspace x { model { s = softwareSystem S {
		a = component "A" "desc" "Go" "Core,Important" {} } } }`)
	c, _ := m.Comp("a")
	if len(c.Tags) != 2 || c.Tags[0] != "Core" {
		t.Errorf("positional tags = %+v, want [Core Important]", c.Tags)
	}
}
