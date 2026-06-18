package mapping

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// Regression: an invalid glob is captured (not silently swallowed) and valid
// globs on the same component still resolve.
func TestInvalidGlobCaptured(t *testing.T) {
	mp := New(model.Model{Components: []model.Component{
		{ID: "a", Paths: []string{"a[", "a/**"}},
	}})
	inv := mp.InvalidPatterns()
	if len(inv) != 1 || inv[0].Pattern != "a[" || inv[0].Comp != "a" {
		t.Fatalf("want 1 invalid pattern a[ for comp a, got %+v", inv)
	}
	if mp.Resolve("a/x.go") != "a" {
		t.Error("the valid glob must still resolve despite the sibling invalid one")
	}
}

// Regression: ResolveDir resolves a package dir owned by a single-file glob.
func TestResolveDirSingleFileGlob(t *testing.T) {
	mp := New(model.Model{Components: []model.Component{
		{ID: "main", Paths: []string{"cmd/app/main.go"}},
	}})
	if got := mp.ResolveDir("cmd/app"); got != "main" {
		t.Errorf("ResolveDir(cmd/app) = %q, want main", got)
	}
}
