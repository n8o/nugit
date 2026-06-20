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
	// components sorted, parented to the system, ids = handles (idempotent)
	if d.ModelObjects[1].ID != "c4" || d.ModelObjects[1].ParentID != rootID {
		t.Errorf("component object wrong: %+v", d.ModelObjects[1])
	}
	// paths carried as tags (binding survives)
	if !reflect.DeepEqual(d.ModelObjects[2].Tags, []string{"internal/render/**"}) {
		t.Errorf("paths not carried as tags: %+v", d.ModelObjects[2])
	}
	// one connection with a stable id
	if len(d.ModelConnections) != 1 || d.ModelConnections[0].ID != "render->c4" {
		t.Fatalf("connection wrong: %+v", d.ModelConnections)
	}
	if d.ModelConnections[0].OriginID != "render" || d.ModelConnections[0].TargetID != "c4" {
		t.Errorf("connection endpoints wrong: %+v", d.ModelConnections[0])
	}

	// idempotent + deterministic: same input -> identical output
	if !reflect.DeepEqual(Transform(m), d) {
		t.Error("non-deterministic transform")
	}
}
