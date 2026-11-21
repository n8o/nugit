package engine

import (
	"strings"
	"testing"
)

const checklistLesson = `---
schema_version: 1
id: LESSON-a-new-field-must-load-from-old-records
type: lesson
scope: global
status: active
created: 2026-01-01T00:00:00Z
provenance:
  commit: x
diff_trigger:
  paths: ["b/**"]
  added: 'json:"'
check: does a record written before this field existed still load?
---

# Lesson — a new field must load from old records

**Trigger:** a serialized type gains a field.

**Insight:** older records do not have it.

**Keywords:** serialization
`

// ADR-0057: a checklist item fires when its paths and its line pattern both
// match the range's diff, and only then.
func TestChecklistItemFiresOnTheDiff(t *testing.T) {
	dir, _ := seed(t, true)
	write(t, dir, ".nugit/lessons/new-field.md", checklistLesson)
	base := commitAll(t, dir, "add a checklist item")

	write(t, dir, "b/b.go", "package b\n\n// B is b.\nfunc B() {}\n")
	quiet := commitAll(t, dir, "touch b without a field")
	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checklist) != 0 {
		t.Fatalf("paths match but no added line does: %+v", rep.Checklist)
	}

	write(t, dir, "b/b.go", "package b\n\ntype Msg struct {\n\tAttempt int `json:\"attempt\"`\n}\n\nfunc B() {}\n")
	head := commitAll(t, dir, "add a field")
	rep, err = BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checklist) != 1 {
		t.Fatalf("want the item to fire: %+v", rep.Checklist)
	}
	m := rep.Checklist[0]
	if m.ID != "LESSON-a-new-field-must-load-from-old-records" || m.File != "b/b.go" || m.Side != "added" ||
		!strings.Contains(m.Line, `json:"attempt"`) {
		t.Errorf("match: %+v", m)
	}
	if len(findings(rep, "model-health")) != 0 {
		t.Errorf("a well-formed item is healthy: %+v", rep.Findings)
	}
}

// An item this PR adds with nothing to ask is reported where it was written.
func TestChecklistItemWithoutACheckIsReported(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/lessons/new-field.md",
		strings.Replace(checklistLesson, "check: does a record written before this field existed still load?\n", "", 1))
	head := commitAll(t, dir, "an item with no question")
	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	fs := findings(rep, "model-health")
	if len(fs) != 1 || !strings.Contains(fs[0].Title, "no check:") {
		t.Fatalf("want one model-health finding for the missing check:, got %+v", fs)
	}
}

// A nested nugit root (apps/op/ inside a larger repo): the store is read at
// the ref and the item fires on the git-root-relative path it names.
func TestChecklistItemFiresInANestedRoot(t *testing.T) {
	root, opDir, _ := seedNested(t)
	item := strings.Replace(checklistLesson, `paths: ["b/**"]`, `paths: ["apps/op/b/**"]`, 1)
	write(t, root, "apps/op/.nugit/lessons/new-field.md", item)
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "item")
	base := git(t, root, "rev-parse", "HEAD")
	write(t, root, "apps/op/b/b.go", "package b\n\ntype Msg struct {\n\tAttempt int `json:\"attempt\"`\n}\n\nfunc B() {}\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "field")
	head := git(t, root, "rev-parse", "HEAD")
	rep, err := BuildReport(Options{RepoDir: opDir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checklist) != 1 || rep.Checklist[0].File != "apps/op/b/b.go" {
		t.Fatalf("the item must fire from a nested root: %+v", rep.Checklist)
	}
}
