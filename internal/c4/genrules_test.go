package c4

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestGenArchLint(t *testing.T) {
	m := model.Model{
		Components: []model.Component{
			{ID: "consistency", Paths: []string{"internal/consistency/**"}},
			{ID: "model_", Paths: []string{"internal/model/**"}},
			{ID: "gitutil", Paths: []string{"internal/gitutil/**"}},
		},
		Relationships: []model.Relationship{
			{Src: "consistency", Dst: "model_"},
			{Src: "consistency", Dst: "gitutil"},
		},
	}
	out := GenArchLint(m)

	if !strings.Contains(out, "version: 3") {
		t.Error("missing go-arch-lint v3 header")
	}
	if !strings.Contains(out, "  consistency:\n    in: internal/consistency/**") {
		t.Errorf("component binding wrong:\n%s", out)
	}
	// consistency may depend on gitutil + model_ (sorted), nothing else
	if !strings.Contains(out, "mayDependOn:\n      - gitutil\n      - model_") {
		t.Errorf("deps not emitted/sorted:\n%s", out)
	}
	// a leaf component depends on nothing internal
	if !strings.Contains(out, "  model_:\n    mayDependOn: []") {
		t.Errorf("leaf component should have empty mayDependOn:\n%s", out)
	}
	// deterministic
	if GenArchLint(m) != out {
		t.Error("non-deterministic output")
	}
}
