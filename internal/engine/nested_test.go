package engine

import (
	"path/filepath"
	"testing"

	"github.com/n8o/nugit/internal/scaffold"
)

// seedNested makes a git repo whose Go module + .nugit/ live at apps/op/ (no
// root go.mod), bootstraps the model there, and returns (gitRoot, opDir, baseSHA).
func seedNested(t *testing.T) (root, opDir, base string) {
	t.Helper()
	root = t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "README.md", "# polyglot monorepo\n")
	write(t, root, "apps/op/go.mod", "module example.com/op\n\ngo 1.25\n")
	write(t, root, "apps/op/a/a.go", "package a\n\nimport _ \"example.com/op/b\"\n\nfunc A() {}\n")
	write(t, root, "apps/op/b/b.go", "package b\n\nfunc B() {}\n")

	opDir = filepath.Join(root, "apps", "op")
	if _, err := scaffold.Run(scaffold.Options{RepoDir: opDir, Mode: "enforce"}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init + code")
	return root, opDir, git(t, root, "rev-parse", "HEAD")
}

// A nested module: a clean change over a declared edge must GROUP under its
// component (not orphan) and stay clean — proving git-root-relative path bridging.
func TestNestedModuleGroupsAndStaysClean(t *testing.T) {
	root, opDir, base := seedNested(t)
	write(t, root, "apps/op/a/a.go", "package a\n\nimport _ \"example.com/op/b\"\n\n// A does a.\nfunc A() {}\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "tweak a")
	head := git(t, root, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: opDir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	var grouped bool
	for _, f := range rep.Code.Files {
		if f.Path == "apps/op/a/a.go" {
			if f.Component == "" {
				t.Fatalf("nested-module file orphaned (the old bug); got component=%q", f.Component)
			}
			grouped = true
		}
	}
	if !grouped {
		t.Fatalf("changed nested file missing from code delta: %+v", rep.Code.Files)
	}
	if f := hasC4Fail(rep); f != nil {
		t.Fatalf("declared-edge change should be clean; got %q", f.Title)
	}
}

// A nested module: an undeclared cross-component import inside the subtree must
// still be flagged — proving the module-prefix bridge in checkC4Code.
func TestNestedModuleUndeclaredEdgeFlagged(t *testing.T) {
	root, opDir, base := seedNested(t)
	// b now imports a (b->a is not in the model, which only declares a->b)
	write(t, root, "apps/op/b/b.go", "package b\n\nimport _ \"example.com/op/a\"\n\nfunc B() {}\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "feat: b uses a")
	head := git(t, root, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: opDir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	f := hasC4Fail(rep)
	if f == nil {
		t.Fatalf("undeclared edge in a nested module must be flagged; findings=%+v", rep.Findings)
	}
}
