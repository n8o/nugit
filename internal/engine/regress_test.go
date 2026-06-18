package engine

import (
	"testing"

	"github.com/burrowfarm/nugit/internal/model"
)

// Regression: a cross-component import from a _test.go file must NOT be flagged
// as an undeclared architecture dependency.
func TestTestFileImportNotFlagged(t *testing.T) {
	dir, base := seed(t, false) // model does NOT declare compa -> compb
	write(t, dir, "a/a_test.go", "package a\n\nimport _ \"example.com/demo/b\"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "test: a_test imports b")
	head := git(t, dir, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if f := hasC4Fail(rep); f != nil {
		t.Fatalf("test-file import must not fail c4<->code; got %q", f.Title)
	}
}

// Regression: a path containing a space gets its churn counted and component
// resolved (validates the -z git parsing fix).
func TestSpaceInPathChurnAndMapping(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, "go.mod", "module example.com/demo\n\ngo 1.25\n")
	write(t, dir, "pkg/my file.go", "package pkg\n")
	write(t, dir, ".nugit/architecture/workspace.dsl",
		"workspace \"d\" { model { s = softwareSystem \"d\" {\n"+
			"  p = component \"P\" { properties { paths \"pkg/**\" } }\n"+
			"} } }")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "pkg/my file.go", "package pkg\n\nfunc X() {}\nfunc Y() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "edit")
	head := git(t, dir, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	var fc *model.FileChange
	for i := range rep.Code.Files {
		if rep.Code.Files[i].Path == "pkg/my file.go" {
			fc = &rep.Code.Files[i]
		}
	}
	if fc == nil {
		t.Fatalf("space-containing path missing from code delta: %+v", rep.Code.Files)
	}
	if fc.Added == 0 {
		t.Errorf("churn for space-containing path should be > 0, got +%d", fc.Added)
	}
	if fc.Component != "p" {
		t.Errorf("space-containing path component = %q, want p", fc.Component)
	}
}
