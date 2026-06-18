package mapping

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func mk() *Mapper {
	return New(model.Model{Components: []model.Component{
		{ID: "a", Paths: []string{"internal/a/**"}},
		{ID: "deep", Paths: []string{"internal/a/deep/**"}},
		{ID: "cli", Paths: []string{"cmd/nugit/**"}},
	}})
}

func TestResolve(t *testing.T) {
	mp := mk()
	cases := map[string]string{
		"internal/a/x.go":      "a",
		"internal/a/deep/y.go": "deep", // most-specific glob wins
		"cmd/nugit/main.go":    "cli",
		"other/z.go":           "",  // orphan
		"./internal/a/x.go":    "a", // leading ./ tolerated
	}
	for path, want := range cases {
		if got := mp.Resolve(path); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestResolveDir(t *testing.T) {
	mp := mk()
	if got := mp.ResolveDir("internal/a/deep"); got != "deep" {
		t.Errorf("ResolveDir(internal/a/deep) = %q, want deep", got)
	}
	if got := mp.ResolveDir("internal/a"); got != "a" {
		t.Errorf("ResolveDir(internal/a) = %q, want a", got)
	}
}

func TestEmpty(t *testing.T) {
	if !New(model.Model{}).Empty() {
		t.Error("empty model should yield empty mapper")
	}
}
