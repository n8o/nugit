package reinforce

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

const targetLesson = `---
schema_version: 1
id: LESSON-tags-reaped
type: lesson
scope: registry
status: active
created: 2026-06-24T00:00:00Z
provenance:
  commit: abc
---

# Lesson — GC reaps non-protected semver tags

**Insight:** protect semver tags in the keep list.
`

func writeStore(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReinforce(t *testing.T) {
	dir := writeStore(t, map[string]string{
		".nugit/lessons/tags-reaped.md": targetLesson,
	})
	res, err := Reinforce(Options{
		RepoDir:  dir,
		ID:       "LESSON-tags-reaped",
		Text:     "every tag family a workflow pushes-then-pulls must be in the keep-tags allowlist",
		Keywords: []string{"precheck", "base-image", "keep-tags"},
		Now:      "2026-08-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "LESSON-tags-reaped-R1" {
		t.Errorf("id: %q", res.ID)
	}
	if res.Path != filepath.Join(".nugit", "lessons", "tags-reaped-r1.md") {
		t.Errorf("path: %q", res.Path)
	}

	// The target file is byte-identical (ADR-0003: never mutated).
	b, _ := os.ReadFile(filepath.Join(dir, ".nugit/lessons/tags-reaped.md"))
	if string(b) != targetLesson {
		t.Fatal("target was mutated — reinforcement must be append-only")
	}

	// The minted object parses, is proposed (candidate lane), inherits scope,
	// carries the edge, and its body holds the widened keywords.
	objs, err := knowledge.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var r, target *model.KnowledgeObject
	for i := range objs {
		switch objs[i].ID {
		case "LESSON-tags-reaped-R1":
			r = &objs[i]
		case "LESSON-tags-reaped":
			target = &objs[i]
		}
	}
	if r == nil || target == nil {
		t.Fatal("minted or target object missing from the store")
	}
	if r.Type != model.KindLesson || r.Status != model.StatusProposed || r.Scope != "registry" {
		t.Errorf("front-matter: type=%s status=%s scope=%s", r.Type, r.Status, r.Scope)
	}
	if len(r.RelatesTo) != 1 || r.RelatesTo[0] != "reinforces:LESSON-tags-reaped" {
		t.Errorf("relates_to: %v", r.RelatesTo)
	}
	if !strings.Contains(r.Body, "precheck") || !strings.Contains(r.Body, "keep-tags") {
		t.Errorf("keywords must land in the body (retrieval matches bodies): %q", r.Body)
	}
	// Loader derives the reverse edge (mirrors AmendedBy).
	if len(target.ReinforcedBy) != 1 || target.ReinforcedBy[0] != "LESSON-tags-reaped-R1" {
		t.Errorf("ReinforcedBy: %v", target.ReinforcedBy)
	}
	if target.EffectiveStatus != model.StatusActive {
		t.Errorf("reinforcement must not change the target's status: %s", target.EffectiveStatus)
	}

	// A second reinforcement gets -R2, and ratified status maps to active.
	res2, err := Reinforce(Options{
		RepoDir: dir, ID: "LESSON-tags-reaped", Text: "second recurrence", Status: "ratified",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res2.ID != "LESSON-tags-reaped-R2" {
		t.Errorf("second id: %q", res2.ID)
	}
	objs, _ = knowledge.Load(dir)
	for i := range objs {
		if objs[i].ID == "LESSON-tags-reaped-R2" && objs[i].Status != model.StatusActive {
			t.Errorf("ratified mint should be active, got %s", objs[i].Status)
		}
	}
}

func TestReinforceRefusals(t *testing.T) {
	superseded := strings.Replace(targetLesson, "id: LESSON-tags-reaped", "id: LESSON-dead", 1)
	successor := strings.Replace(strings.Replace(targetLesson,
		"id: LESSON-tags-reaped", "id: LESSON-new", 1),
		"provenance:", "supersedes: LESSON-dead\nprovenance:", 1)
	dir := writeStore(t, map[string]string{
		".nugit/lessons/dead.md": superseded,
		".nugit/lessons/new.md":  successor,
	})

	if _, err := Reinforce(Options{RepoDir: dir, ID: "LESSON-nope", Text: "x"}); err == nil {
		t.Error("unknown id must error")
	}
	if _, err := Reinforce(Options{RepoDir: dir, ID: "LESSON-dead", Text: "x"}); err == nil ||
		!strings.Contains(err.Error(), "superseded") {
		t.Errorf("superseded target must be refused, got %v", err)
	}
	if _, err := Reinforce(Options{RepoDir: dir, ID: "LESSON-new", Text: ""}); err == nil {
		t.Error("empty -text must error")
	}
	if _, err := Reinforce(Options{RepoDir: dir, ID: "LESSON-new", Text: "x", Status: "bogus"}); err == nil {
		t.Error("unknown status must error")
	}
}
