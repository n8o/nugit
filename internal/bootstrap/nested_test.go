package bootstrap

import (
	"testing"

	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/mapping"
)

// Regression for the critical review finding: edge attribution must use the SAME
// glob resolver the check uses, so a nested import resolves to the nested
// component (not bubbling to the parent), and the generated model is green by
// construction.
func TestNestedPackageEdgesResolveConsistently(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, "go.mod", "module example.com/m\n")
	wf(t, dir, "a/a.go", "package a\n")
	wf(t, dir, "a/b/b.go", "package b\n") // nested package under a
	wf(t, dir, "c/c.go", "package c\n\nimport (\n\t_ \"example.com/m/a\"\n\t_ \"example.com/m/a/b\"\n)\n")

	g, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[[2]string]bool{}
	for _, e := range g.Edges {
		got[e] = true
	}
	if !got[[2]string{"c", "a"}] || !got[[2]string{"c", "b"}] {
		t.Fatalf("want edges c->a and c->b (nested import must not bubble to parent); got %+v", g.Edges)
	}
	if len(g.Edges) != 2 {
		t.Errorf("want exactly 2 edges, got %+v", g.Edges)
	}

	// The generated model, resolved with the check's own mapper, agrees.
	mp := mapping.New(c4.Parse(GenerateDSL(g, "m")))
	if mp.ResolveDir("a/b") != "b" {
		t.Errorf("a/b should resolve to nested component b, got %q", mp.ResolveDir("a/b"))
	}
	if mp.ResolveDir("a") != "a" {
		t.Errorf("a should resolve to a, got %q", mp.ResolveDir("a"))
	}
}
