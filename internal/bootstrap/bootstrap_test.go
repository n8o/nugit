package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n8o/nugit/internal/c4"
)

func wf(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAndGenerate(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, "go.mod", "module example.com/m\n\ngo 1.25\n")
	wf(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/m/b\"\n")
	wf(t, dir, "b/b.go", "package b\n")
	// a TEST import b->a must NOT create an architecture edge
	wf(t, dir, "b/b_test.go", "package b\n\nimport _ \"example.com/m/a\"\n")
	// a build-excluded file's import must NOT create an edge either
	wf(t, dir, "a/ignore.go", "//go:build ignore\n\npackage a\nimport _ \"example.com/m/b\"\n")

	g, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Components) != 2 {
		t.Fatalf("want 2 components (a,b), got %d: %+v", len(g.Components), g.Components)
	}
	if len(g.Edges) != 1 || g.Edges[0] != [2]string{"a", "b"} {
		t.Fatalf("want exactly edge a->b, got %+v", g.Edges)
	}

	// The generated DSL must round-trip and reproduce the graph exactly.
	m := c4.Parse(GenerateDSL(g, "m"))
	if !m.HasRelationship("a", "b") {
		t.Errorf("generated DSL missing a->b: %+v", m.Relationships)
	}
	if m.HasRelationship("b", "a") {
		t.Errorf("generated DSL leaked b->a (from a test import)")
	}
	ca, ok := m.Comp("a")
	if !ok || len(ca.Paths) != 1 || ca.Paths[0] != "a/**" {
		t.Errorf("component a paths wrong: %+v", ca)
	}
}

func TestDiscoverEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, "go.mod", "module example.com/m\n")
	wf(t, dir, "README.md", "# no go packages\n")
	g, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Components) != 0 {
		t.Errorf("no Go files should yield no components, got %+v", g.Components)
	}
}

func TestAssignIDsDisambiguates(t *testing.T) {
	ids := assignIDs([]string{"internal/a/util", "internal/b/util"})
	if ids["internal/a/util"] == ids["internal/b/util"] {
		t.Errorf("colliding last segments must get distinct ids: %v", ids)
	}
}
