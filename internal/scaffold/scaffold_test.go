package scaffold

import (
	"os"
	"path/filepath"
	"strings"
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

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunBootstrap(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, "go.mod", "module example.com/m\n\ngo 1.25\n")
	wf(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/m/b\"\n")
	wf(t, dir, "b/b.go", "package b\n")

	res, err := Run(Options{RepoDir: dir, Mode: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Components != 2 || res.Edges != 1 {
		t.Errorf("want 2 components / 1 edge, got %d/%d", res.Components, res.Edges)
	}

	// generated model round-trips and has the real edge
	m := c4.Parse(read(t, dir, ".nugit/architecture/workspace.dsl"))
	if !m.HasRelationship("a", "b") {
		t.Error("scaffolded model missing a->b")
	}
	// config carries the warn mode; gitignore carries the nugit lines
	if !strings.Contains(read(t, dir, ".nugit/config.yml"), "mode: warn") {
		t.Error("config.yml should record mode: warn")
	}
	if gi := read(t, dir, ".gitignore"); !strings.Contains(gi, ".nugit-local/") {
		t.Errorf(".gitignore missing nugit lines: %q", gi)
	}

	// idempotent: a second run skips existing files (no clobber)
	res2, err := Run(Options{RepoDir: dir, Mode: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Skipped) == 0 {
		t.Error("re-running init should skip existing files, not recreate them")
	}
}

func TestRunNoModel(t *testing.T) {
	dir := t.TempDir()
	res, err := Run(Options{RepoDir: dir, NoModel: true, Mode: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Components != 0 {
		t.Errorf("--no-model should not discover components, got %d", res.Components)
	}
	dsl := read(t, dir, ".nugit/architecture/workspace.dsl")
	if !strings.Contains(dsl, "softwareSystem") {
		t.Error("template DSL should still be a valid skeleton")
	}
}

func TestInvalidMode(t *testing.T) {
	if _, err := Run(Options{RepoDir: t.TempDir(), Mode: "bogus"}); err == nil {
		t.Error("an invalid -mode should error")
	}
}
