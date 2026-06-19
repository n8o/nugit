package engine

import (
	"path/filepath"
	"testing"

	"github.com/n8o/nugit/internal/scaffold"
)

// Fix 1 (critical): a nested module's subpackage importing the module ROOT
// package is an undeclared edge that must be flagged — the prefix+"." path must
// normalize (path.Clean) so it doesn't silently drop.
func TestNestedRootPackageImportFlagged(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "apps/op/go.mod", "module example.com/op\n\ngo 1.25\n")
	write(t, root, "apps/op/root.go", "package op\n\nfunc Root() {}\n")
	write(t, root, "apps/op/sub/sub.go", "package sub\n\nfunc S() {}\n")
	opDir := filepath.Join(root, "apps", "op")
	if _, err := scaffold.Run(scaffold.Options{RepoDir: opDir, Mode: "enforce"}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	base := git(t, root, "rev-parse", "HEAD")

	// sub now imports the module root package — undeclared edge sub -> root.
	write(t, root, "apps/op/sub/sub.go", "package sub\n\nimport _ \"example.com/op\"\n\nfunc S() {}\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "feat: sub uses root")
	head := git(t, root, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: opDir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if hasC4Fail(rep) == nil {
		t.Fatalf("nested root-package import must be flagged; findings=%+v", rep.Findings)
	}
}

// Fix 2 (high): a structural model declares no edges on purpose — a real Go
// cross-component import must NOT be graded by c4<->code (would be a false FAIL
// in enforce mode).
func TestStructuralModelNotGraded(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.25\n")
	write(t, root, "apps/a/a.go", "package a\n\nimport _ \"example.com/m/libs/b\"\n\nfunc A() {}\n")
	write(t, root, "libs/b/b.go", "package b\n\nfunc B() {}\n")
	// Force a STRUCTURAL model (edges-free) even though Go imports exist.
	if _, err := scaffold.Run(scaffold.Options{RepoDir: root, Mode: "enforce", Layout: "container"}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	base := git(t, root, "rev-parse", "HEAD")

	write(t, root, "apps/a/a.go", "package a\n\nimport _ \"example.com/m/libs/b\"\n\n// tweak.\nfunc A() {}\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "tweak")
	head := git(t, root, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: root, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Check == "c4<->code" {
			t.Fatalf("structural model must not be graded by c4<->code; got %q", f.Title)
		}
	}
}
