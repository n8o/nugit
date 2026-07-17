package c4

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// leveledSrc is a fully anonymized two-level fixture: containers with nested
// components, a container with its own paths, a group-wrapped container, and a
// blockless container.
const leveledSrc = `workspace "m" {
  model {
    s = softwareSystem "m" {
      group "Services" {
        ingest = container "Ingest" "reads input" "Go" "Core,Edge" {
          x1 = component "X1" { properties { paths "x1/**" } }
          x2 = component "X2" { properties { paths "x2/**" } }
          x1 -> x2 "internal hop"
        }
      }
      encoder = container "Encoder" {
        technology "C++"
        tags "Heavy"
        properties {
          "paths" "enc/**"
        }
        group "inner" {
          y1 = component "Y1" { properties { paths "y1/**" } }
        }
      }
      publisher = container "Publisher"
      x1 -> y1
    }
  }
}`

func TestContainersRecorded(t *testing.T) {
	m := Parse(leveledSrc)
	if len(m.Containers) != 3 {
		t.Fatalf("want 3 containers, got %d: %+v", len(m.Containers), m.Containers)
	}
	ing, ok := m.Container("ingest")
	if !ok {
		t.Fatal("container ingest not recorded")
	}
	if ing.Name != "Ingest" || ing.Tech != "Go" || !reflect.DeepEqual(ing.Tags, []string{"Core", "Edge"}) {
		t.Errorf("ingest metadata wrong: %+v", ing)
	}
	enc, ok := m.Container("encoder")
	if !ok {
		t.Fatal("container encoder not recorded")
	}
	if enc.Tech != "C++" || !reflect.DeepEqual(enc.Tags, []string{"Heavy"}) {
		t.Errorf("encoder block metadata wrong: %+v", enc)
	}
	if !reflect.DeepEqual(enc.Paths, []string{"enc/**"}) {
		t.Errorf("encoder own paths wrong: %+v", enc.Paths)
	}
	pub, ok := m.Container("publisher")
	if !ok {
		t.Fatal("blockless container publisher not recorded")
	}
	if len(pub.Paths) != 0 || pub.Name != "Publisher" {
		t.Errorf("blockless container wrong: %+v", pub)
	}
}

func TestComponentParentContainerSet(t *testing.T) {
	m := Parse(leveledSrc)
	want := map[string]string{"x1": "ingest", "x2": "ingest", "y1": "encoder"}
	if len(m.Components) != len(want) {
		t.Fatalf("want %d components, got %+v", len(want), m.Components)
	}
	for id, parent := range want {
		if got := m.ContainerOf(id); got != parent {
			t.Errorf("ContainerOf(%s) = %q, want %q", id, got, parent)
		}
	}
	// A group inside a container is transparent: y1 belongs to encoder.
	if c, _ := m.Comp("y1"); c.Container != "encoder" || c.Paths[0] != "y1/**" {
		t.Errorf("group-wrapped component wrong: %+v", c)
	}
	// Relationships inside container/group bodies are recorded.
	if !m.HasRelationship("x1", "x2") || !m.HasRelationship("x1", "y1") {
		t.Errorf("relationships lost: %+v", m.Relationships)
	}
}

// Container-level paths bind the CONTAINER, never the model properties — but
// every other container-level property key must still reach Model.Properties:
// `nugit init` emits `"nugit_structural" "true"` inside the container block and
// Structural() depends on it (the historical transparent-container leak).
func TestContainerPropertiesRouting(t *testing.T) {
	src := `workspace "m" {
	  model {
	    sys = softwareSystem "m" {
	      app = container "m" {
	        properties {
	          "nugit_structural" "true"
	          "paths" "app/**"
	        }
	        a = component "A" { properties { paths "a/**" } }
	      }
	    }
	  }
	}`
	m := Parse(src)
	if !m.Structural() {
		t.Fatal("nugit_structural inside a container block must reach Model.Properties (Structural() regression)")
	}
	if got := m.Properties["paths"]; got != "" {
		t.Errorf("container paths leaked into model properties: %q", got)
	}
	ct, _ := m.Container("app")
	if len(ct.Paths) != 1 || ct.Paths[0] != "app/**" {
		t.Errorf("container paths not bound: %+v", ct.Paths)
	}
	if a, _ := m.Comp("a"); len(a.Paths) != 1 || a.Paths[0] != "a/**" || a.Container != "app" {
		t.Errorf("component inside container wrong: %+v", a)
	}
}

// nugit's own model: the `app` wrapper container is recorded in Containers —
// and stays out of Components (byte-identity for every flat consumer).
func TestOwnModelAppContainer(t *testing.T) {
	b, err := os.ReadFile("../../.nugit/architecture/workspace.dsl")
	if err != nil {
		t.Fatalf("read own workspace.dsl: %v", err)
	}
	m := Parse(string(b))
	if _, ok := m.Container("app"); !ok {
		t.Error("own model: `app` container not recorded in Containers")
	}
	if len(m.Containers) != 1 {
		t.Errorf("own model: want exactly 1 container, got %+v", m.Containers)
	}
	if _, ok := m.Comp("app"); ok {
		t.Error("own model: `app` leaked into Components")
	}
	if len(m.Components) == 0 {
		t.Error("own model: components lost")
	}
	for _, c := range m.Components {
		if c.Container != "app" {
			t.Errorf("own model: component %s parent = %q, want app", c.ID, c.Container)
		}
	}
}

func TestContainerDiff(t *testing.T) {
	base := Parse(`workspace m { model { s = softwareSystem S {
		x = container "X" { properties { "paths" "x/**" } }
		y = container "Y" } } }`)
	head := Parse(`workspace m { model { s = softwareSystem S {
		x = container "X2" { properties { "paths" "x/**,x2/**" } }
		z = container "Z" } } }`)
	d := Diff(base, head, "")
	if len(d.AddedContainers) != 1 || d.AddedContainers[0].ID != "z" {
		t.Errorf("added containers wrong: %+v", d.AddedContainers)
	}
	if len(d.RemovedContainers) != 1 || d.RemovedContainers[0].ID != "y" {
		t.Errorf("removed containers wrong: %+v", d.RemovedContainers)
	}
	if len(d.ChangedContainers) != 1 {
		t.Fatalf("changed containers wrong: %+v", d.ChangedContainers)
	}
	cc := d.ChangedContainers[0]
	if cc.After.ID != "x" || !reflect.DeepEqual(cc.Fields, []string{"name", "paths"}) {
		t.Errorf("changed fields wrong: %+v", cc)
	}
	if d.Empty() {
		t.Error("container-only delta must not be Empty() — container changes are architectural")
	}
	// Container-only change: no component noise.
	if len(d.AddedComponents)+len(d.RemovedComponents)+len(d.ChangedComponents) != 0 {
		t.Errorf("component delta leaked: %+v", d)
	}
}

// A container-only delta renders in MermaidDiff as plain nodes (subgraphs are
// a follow-up), styled like component adds/removes.
func TestMermaidDiffContainerNodes(t *testing.T) {
	head := Parse(`workspace m { model { s = softwareSystem S {
		x = container "X Box" { a = component "A" } } } }`)
	d := model.C4Delta{
		AddedContainers:   []model.Container{{ID: "x", Name: "X Box"}},
		RemovedContainers: []model.Container{{ID: "old"}},
	}
	got := MermaidDiff(d, head)
	if got == "" {
		t.Fatal("container-only delta must render a diagram (Empty() is false)")
	}
	if !strings.Contains(got, `x["X Box"]:::add`) {
		t.Errorf("added container node missing:\n%s", got)
	}
	if !strings.Contains(got, `old["old"]:::rem`) {
		t.Errorf("removed container node missing:\n%s", got)
	}
}

func TestContainerOnlyChangeNotEmpty(t *testing.T) {
	base := Parse(`workspace m { model { s = softwareSystem S { x = container "X" } } }`)
	head := Parse(`workspace m { model { s = softwareSystem S { x = container "X" "d" "Go" } } }`)
	d := Diff(base, head, "")
	if len(d.ChangedContainers) != 1 || d.Empty() {
		t.Fatalf("tech-only container change must be a non-empty delta: %+v", d)
	}
}
