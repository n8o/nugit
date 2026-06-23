package c4

import "testing"

// A `container` is a transparent C4 grouping: the parser must descend into it and
// record the components inside (with paths + relationships), but NOT record the
// container itself. This is what lets nugit emit valid Structurizr DSL
// (system → container → component) while keeping its component-centric model.
func TestContainerIsTransparent(t *testing.T) {
	// The quoted-key, multi-line properties form is exactly what nugit emits for
	// Structurizr compatibility — the parser must read paths out of it.
	src := `workspace "x" {
	  model {
	    s = softwareSystem "S" {
	      app = container "App" {
	        a = component "A" {
	          properties {
	            "paths" "a/**"
	          }
	        }
	        b = component "B" {
	          properties {
	            "paths" "b/**"
	          }
	        }
	        a -> b
	      }
	    }
	  }
	}`
	m := Parse(src)
	if len(m.Components) != 2 {
		t.Fatalf("container not transparent: want 2 components, got %d (%+v)", len(m.Components), m.Components)
	}
	var a *struct{ paths []string }
	for _, c := range m.Components {
		if c.ID == "a" {
			a = &struct{ paths []string }{c.Paths}
		}
		if c.ID == "app" {
			t.Errorf("container element 'app' must not be recorded as a component")
		}
	}
	if a == nil || len(a.paths) != 1 || a.paths[0] != "a/**" {
		t.Errorf("paths not preserved through container nesting: %+v", m.Components)
	}
	if len(m.Relationships) != 1 || m.Relationships[0].Src != "a" || m.Relationships[0].Dst != "b" {
		t.Errorf("relationship not parsed through container: %+v", m.Relationships)
	}
}

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
