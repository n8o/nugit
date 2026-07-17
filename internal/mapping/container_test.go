package mapping

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestContainerGlobsResolve(t *testing.T) {
	m := model.Model{
		Components: []model.Component{
			{ID: "y1", Paths: []string{"y1/**"}, Container: "Y"},
		},
		Containers: []model.Container{
			{ID: "Y", Paths: []string{"ycore/**"}},
			{ID: "Z"}, // paths-less container: contributes no rules
		},
	}
	mp := New(m)
	if got := mp.Resolve("ycore/core.go"); got != "Y" {
		t.Errorf("container glob did not resolve: got %q, want Y", got)
	}
	if got := mp.Resolve("y1/a.go"); got != "y1" {
		t.Errorf("component glob broken: got %q", got)
	}
	if got := mp.ResolveDir("ycore"); got != "Y" {
		t.Errorf("ResolveDir on container glob: got %q, want Y", got)
	}
	if got := mp.Resolve("elsewhere/x.go"); got != "" {
		t.Errorf("unowned path resolved to %q", got)
	}
}

// At equal literal-prefix specificity the component beats the container: the
// finer element owns the file.
func TestEqualSpecificityTieComponentWins(t *testing.T) {
	m := model.Model{
		Components: []model.Component{
			{ID: "svc_c", Paths: []string{"svc/**"}, Container: "svc"},
		},
		Containers: []model.Container{
			{ID: "svc2", Paths: []string{"svc/**"}},
		},
	}
	mp := New(m)
	if got := mp.Resolve("svc/main.go"); got != "svc_c" {
		t.Errorf("tie must go to the component: got %q, want svc_c", got)
	}
	// A strictly more specific container glob still wins over a shallower
	// component glob (specificity first, level only breaks ties).
	m2 := model.Model{
		Components: []model.Component{{ID: "root", Paths: []string{"svc/**"}}},
		Containers: []model.Container{{ID: "deep", Paths: []string{"svc/deep/**"}}},
	}
	mp2 := New(m2)
	if got := mp2.Resolve("svc/deep/f.go"); got != "deep" {
		t.Errorf("deeper container glob must win: got %q, want deep", got)
	}
}

func TestInvalidPatternKinds(t *testing.T) {
	m := model.Model{
		Components: []model.Component{{ID: "a", Paths: []string{"a/[bad"}}},
		Containers: []model.Container{{ID: "X", Paths: []string{"x/[bad"}}},
	}
	bad := New(m).InvalidPatterns()
	if len(bad) != 2 {
		t.Fatalf("want 2 invalid patterns, got %+v", bad)
	}
	// sorted by Comp: "X" < "a"
	if bad[0].Comp != "X" || bad[0].Kind != "container" {
		t.Errorf("container invalid pattern wrong: %+v", bad[0])
	}
	if bad[1].Comp != "a" || bad[1].Kind != "component" {
		t.Errorf("component invalid pattern wrong: %+v", bad[1])
	}
}
