package retrieval

import (
	"strings"
	"testing"
)

// ADR-0057: a checklist item is served for a file under its trigger paths,
// whatever the task says, and carries its check: question.
func TestChecklistItemServedForFilesInPlay(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/lessons/migrations.md", "---\nschema_version: 1\nid: LESSON-migrations-run-twice\n"+
		"type: lesson\nscope: global\nstatus: active\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n"+
		"diff_trigger:\n  paths: [\"db/migrations/**\"]\n  added: \"ALTER TABLE\"\n"+
		"check: is the migration safe to run twice?\n---\n\n# Lesson — migrations run twice\n\n"+
		"**Insight:** a retried deploy reapplies them.\n")
	b, err := Context(Options{RepoDir: dir, Path: "db/migrations/0042_add_col.sql", Task: "zzz qqq"})
	if err != nil {
		t.Fatal(err)
	}
	var got *Item
	for i := range b.Lessons {
		if b.Lessons[i].ID == "LESSON-migrations-run-twice" {
			got = &b.Lessons[i]
		}
	}
	if got == nil || got.Check != "is the migration safe to run twice?" || !got.PathBound {
		t.Fatalf("the item must be served, path-bound, with its question: %+v", b.Lessons)
	}
	if md := b.Markdown(); !strings.Contains(md, "check: is the migration safe to run twice?") {
		t.Errorf("markdown must carry the question:\n%s", md)
	}
	b2, err := Context(Options{RepoDir: dir, Path: "README.md", Task: "zzz qqq"})
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range b2.Lessons {
		if l.ID == "LESSON-migrations-run-twice" {
			t.Errorf("outside its paths with an unrelated task the item is not served: %+v", l)
		}
	}
}
