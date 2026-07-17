package ratify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
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

func obj(id, typ, status string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: " + typ + "\nscope: a\nstatus: " + status +
		"\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# " + id + "\n\nbody with the word proposed in it\n"
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Ratify must change exactly the status line and nothing else — including a
// `proposed` occurrence in the body.
func TestRatifySurgicalEdit(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/d.md", obj("ADR-D", "decision", "proposed"))
	wf(t, dir, ".nugit/lessons/l.md", obj("LESSON-L", "lesson", "proposed"))

	before := read(t, dir, ".nugit/decisions/d.md")
	res, err := Ratify(dir, "ADR-D")
	if err != nil {
		t.Fatal(err)
	}
	if res.From != model.StatusProposed || res.To != model.StatusAccepted {
		t.Errorf("decision must ratify proposed -> accepted, got %s -> %s", res.From, res.To)
	}
	after := read(t, dir, ".nugit/decisions/d.md")
	want := strings.Replace(before, "status: proposed", "status: accepted", 1)
	if after != want {
		t.Errorf("ratify must be a one-line edit.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !strings.Contains(after, "body with the word proposed in it") {
		t.Error("body must be untouched")
	}

	res2, err := Ratify(dir, "LESSON-L")
	if err != nil {
		t.Fatal(err)
	}
	if res2.To != model.StatusActive {
		t.Errorf("lesson must ratify to active, got %s", res2.To)
	}
}

func TestRatifyRefusals(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/a.md", obj("ADR-A", "decision", "accepted"))
	wf(t, dir, ".nugit/decisions/p.md", obj("ADR-P", "decision", "proposed"))
	wf(t, dir, ".nugit/decisions/s.md", // supersedes the proposed candidate
		"---\nschema_version: 1\nid: ADR-S\ntype: decision\nscope: a\nstatus: accepted\ncreated: 2026-01-02T00:00:00Z\nsupersedes: ADR-P\nprovenance:\n  commit: x\n---\n\n# ADR-S\n")

	if _, err := Ratify(dir, "ADR-A"); err == nil {
		t.Error("ratifying an already-accepted object must error")
	}
	if _, err := Ratify(dir, "ADR-NOPE"); err == nil {
		t.Error("unknown id must error")
	}
	if _, err := Ratify(dir, "ADR-P"); err == nil {
		t.Error("a superseded proposed object must refuse ratification")
	}
}

func TestPendingOrderingAndSkips(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/z.md", obj("ADR-Z", "decision", "proposed"))
	wf(t, dir, ".nugit/lessons/a.md", obj("LESSON-A", "lesson", "proposed"))
	wf(t, dir, ".nugit/decisions/ok.md", obj("ADR-OK", "decision", "accepted"))
	// Malformed front-matter (list-form supersedes breaks the schema): parsed
	// as an untyped object; Pending must skip it, not crash.
	wf(t, dir, ".nugit/decisions/bad.md",
		"---\nid: ADR-BAD\nsupersedes:\n  - ADR-X\n  - ADR-Y\nstatus: proposed\n---\n\n# bad\n")

	pending, err := Pending(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range pending {
		got = append(got, o.ID)
	}
	if len(got) != 2 || got[0] != "ADR-Z" || got[1] != "LESSON-A" {
		// sorted by id: ADR-Z < LESSON-A
		t.Errorf("want [ADR-Z LESSON-A], got %v", got)
	}
}

func TestPromoteRejectsAmbiguity(t *testing.T) {
	if _, err := promote("no fence here", model.StatusAccepted); err == nil {
		t.Error("missing fence must error")
	}
	if _, err := promote("---\nid: x\nstatus: accepted\n---\n", model.StatusAccepted); err == nil {
		t.Error("zero proposed lines must error")
	}
	if _, err := promote("---\nstatus: proposed\nstatus: proposed\n---\n", model.StatusAccepted); err == nil {
		t.Error("multiple proposed lines must error")
	}
	// CRLF line endings are preserved.
	out, err := promote("---\r\nid: x\r\nstatus: proposed\r\n---\r\n\r\nbody\r\n", model.StatusAccepted)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "status: accepted\r\n") {
		t.Errorf("CRLF must be preserved, got %q", out)
	}
}
