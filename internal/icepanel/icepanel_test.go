package icepanel

import (
	"reflect"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestTransform(t *testing.T) {
	m := model.Model{
		Name: "nugit",
		Components: []model.Component{
			{ID: "render", Name: "Renderer", Tech: "Go", Paths: []string{"internal/render/**"}},
			{ID: "c4", Name: "C4", Paths: []string{"internal/c4/**"}},
		},
		Relationships: []model.Relationship{{Src: "render", Dst: "c4", Desc: "renders diagrams"}},
	}
	d := Transform(m)

	// one system root + 2 components
	if len(d.ModelObjects) != 3 {
		t.Fatalf("want 3 objects (system + 2), got %d", len(d.ModelObjects))
	}
	if d.ModelObjects[0].Type != "system" || d.ModelObjects[0].Name != "nugit" {
		t.Errorf("first object should be the system root: %+v", d.ModelObjects[0])
	}
	// components sorted, typed, parented to the system, ids = handles (idempotent)
	if d.ModelObjects[1].ID != "c4" || d.ModelObjects[1].Type != "component" || d.ModelObjects[1].ParentID != rootID {
		t.Errorf("component object wrong: %+v", d.ModelObjects[1])
	}
	// paths carried in description (binding survives; tech prefixed)
	if d.ModelObjects[2].Description != "Go — internal/render/**" {
		t.Errorf("paths not carried in description: %q", d.ModelObjects[2].Description)
	}
	// one connection: stable id, required direction, name from desc
	c := d.ModelConnections[0]
	if len(d.ModelConnections) != 1 || c.ID != "render->c4" || c.Direction != "outgoing" {
		t.Fatalf("connection wrong: %+v", d.ModelConnections)
	}
	if c.OriginID != "render" || c.TargetID != "c4" || c.Name != "renders diagrams" {
		t.Errorf("connection fields wrong: %+v", c)
	}

	// idempotent + deterministic: same input -> identical output
	if !reflect.DeepEqual(Transform(m), d) {
		t.Error("non-deterministic transform")
	}
}
