package engine

import (
	"testing"

	"github.com/n8o/nugit/internal/scaffold"
)

// Acceptance: `nugit init` bootstraps a model that makes the first pr-render
// green even in enforce mode — a change over a declared edge is clean.
func TestInitBootstrapMakesRenderClean(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, "go.mod", "module example.com/demo\n\ngo 1.25\n")
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\nfunc A() {}\n")
	write(t, dir, "b/b.go", "package b\n\nfunc B() {}\n")

	// bootstrap in enforce mode so a genuine violation WOULD fail
	if _, err := scaffold.Run(scaffold.Options{RepoDir: dir, Mode: "enforce"}); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init + code")
	base := git(t, dir, "rev-parse", "HEAD")

	// a clean change over the DECLARED a->b edge
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\n// A does a.\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "tweak a")
	head := git(t, dir, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if f := hasC4Fail(rep); f != nil {
		t.Fatalf("a bootstrapped model must make a declared-edge change clean; got %q", f.Title)
	}
}
