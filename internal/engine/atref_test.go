package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// Knowledge must be read at the reviewed ref, never the working tree
// (LESSON-read-from-reviewed-ref). These tests diverge the checkout from head
// in both directions and assert pr-render describes base..head regardless.

const staleLesson = "---\nschema_version: 1\nid: LESSON-OLD\ntype: lesson\nscope: compa\nstatus: superseded\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# LESSON-OLD\n"

const specObj = "---\nschema_version: 1\nid: SPEC-001\ntype: spec\nscope: compa\nstatus: active\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# SPEC-001\n"

func findCheck(rep model.Report, check string) *model.Finding {
	for i := range rep.Findings {
		if rep.Findings[i].Check == check {
			return &rep.Findings[i]
		}
	}
	return nil
}

// A stale object present at head still governs the PR even when the working
// tree has deleted it: the finding is a fact about base..head, not the checkout.
func TestStaleKnowledgeReadAtHeadRefNotWorkingTree(t *testing.T) {
	dir, _ := seed(t, false)
	write(t, dir, ".nugit/lessons/old.md", staleLesson)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "chore: record superseded lesson")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "a/a.go", "package a\n\n// A does another thing.\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feat: change a under a stale lesson")
	head := git(t, dir, "rev-parse", "HEAD")

	// Diverge the working tree: the lesson vanishes from disk but not from head.
	if err := os.Remove(filepath.Join(dir, ".nugit", "lessons", "old.md")); err != nil {
		t.Fatal(err)
	}

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	f := findCheck(rep, "stale-knowledge")
	if f == nil {
		t.Fatalf("stale-knowledge must fire per the head ref even when the working tree deleted the object; findings=%+v", rep.Findings)
	}
	if !strings.Contains(f.Title, "LESSON-OLD") {
		t.Errorf("finding should name LESSON-OLD, got %q", f.Title)
	}
}

// The inverse: an object that exists only in the working tree (uncommitted at
// head) is invisible to pr-render — it is not part of the reviewed range.
func TestWorkingTreeOnlyKnowledgeIgnored(t *testing.T) {
	dir, base := seed(t, false)
	write(t, dir, "a/a.go", "package a\n\n// A does another thing.\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feat: change a")
	head := git(t, dir, "rev-parse", "HEAD")

	// Uncommitted working-tree-only stale lesson governing compa.
	write(t, dir, ".nugit/lessons/old.md", staleLesson)

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if f := findCheck(rep, "stale-knowledge"); f != nil {
		t.Fatalf("an uncommitted working-tree object must not produce findings about the reviewed range; got %q", f.Title)
	}
}

// A spec present at head satisfies a `spec:` trailer even when the working tree
// has deleted the spec file — no fabricated unknown-spec warning.
func TestSpecLinkageReadAtHeadRefNotWorkingTree(t *testing.T) {
	dir, _ := seed(t, false)
	write(t, dir, ".nugit/specs/001-thing.md", specObj)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "chore: add spec")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "a/a.go", "package a\n\n// A implements the thing.\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feat: implement the thing\n\nspec: SPEC-001")
	head := git(t, dir, "rev-parse", "HEAD")

	if err := os.Remove(filepath.Join(dir, ".nugit", "specs", "001-thing.md")); err != nil {
		t.Fatal(err)
	}

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if f := findCheck(rep, "spec-linkage"); f != nil {
		t.Fatalf("SPEC-001 exists at head; deleting it from the working tree must not fabricate %q", f.Title)
	}
}
