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

	// domain → system → app spine + 2 components = 5 objects
	if len(d.ModelObjects) != 5 {
		t.Fatalf("want 5 objects (domain+system+app + 2 components), got %d", len(d.ModelObjects))
	}
	if d.ModelObjects[0].Type != "domain" || d.ModelObjects[0].ParentID != "" {
		t.Errorf("object[0] must be a top-level domain: %+v", d.ModelObjects[0])
	}
	if d.ModelObjects[1].Type != "system" || d.ModelObjects[1].ParentID != domainID {
		t.Errorf("object[1] must be a system under the domain: %+v", d.ModelObjects[1])
	}
	if d.ModelObjects[2].Type != "app" || d.ModelObjects[2].ParentID != systemID {
		t.Errorf("object[2] must be an app under the system: %+v", d.ModelObjects[2])
	}
	// components sorted, parented to the APP (not the system), ids = handles
	if d.ModelObjects[3].ID != "c4" || d.ModelObjects[3].Type != "component" || d.ModelObjects[3].ParentID != appID {
		t.Errorf("component must hang under the app: %+v", d.ModelObjects[3])
	}
	// paths carried in description (binding survives; tech prefixed)
	if d.ModelObjects[4].Description != "Go — internal/render/**" {
		t.Errorf("paths not carried in description: %q", d.ModelObjects[4].Description)
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
