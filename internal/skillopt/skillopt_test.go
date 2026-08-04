package skillopt

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func writeLesson(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, ".nugit", "lessons", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const frontMatter = `---
schema_version: 1
id: %ID%
type: lesson
scope: delta
status: %STATUS%
created: 2026-07-01T00:00:00Z
%EXTRA%provenance:
  commit: seed
confidence: high
---

`

func lessonFile(id, status, extra, body string) string {
	fm := strings.ReplaceAll(frontMatter, "%ID%", id)
	fm = strings.ReplaceAll(fm, "%STATUS%", status)
	fm = strings.ReplaceAll(fm, "%EXTRA%", extra)
	return fm + body
}

// A superseded lesson holds a DEAD answer. Emitting it as a gold answer would
// train the wrong thing, so the supersedes graph (never a mutated file) excludes
// it — and the refusal is reported, not swallowed.
func TestSupersededLessonExcluded(t *testing.T) {
	dir := t.TempDir()
	writeLesson(t, dir, "old.md", lessonFile("LESSON-old", "active", "", cleanBody))
	writeLesson(t, dir, "new.md", lessonFile("LESSON-new", "active", "supersedes: LESSON-old\n", cleanBody))

	cases, rep, err := Export(Options{RepoDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Number != "lesson:new" {
		t.Fatalf("want only the superseding lesson, got %+v", numbers(cases))
	}
	if rep.Summary.Lessons != 2 || rep.Summary.NeedsAgent != 1 {
		t.Errorf("summary = %+v, want 2 scanned / 1 refused", rep.Summary)
	}
	if len(rep.Refused) != 1 || rep.Refused[0].Reasons[0] != ReasonStale {
		t.Errorf("refused = %+v, want %s", rep.Refused, ReasonStale)
	}
}

// An invalidated lesson is dead for the same reason.
func TestInvalidatedLessonExcluded(t *testing.T) {
	dir := t.TempDir()
	writeLesson(t, dir, "dead.md", lessonFile("LESSON-dead", "invalidated", "", cleanBody))
	cases, rep, err := Export(Options{RepoDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 0 {
		t.Fatalf("an invalidated lesson must not emit: %+v", numbers(cases))
	}
	if rep.Summary.Reasons[ReasonStale] != 1 {
		t.Errorf("reasons = %+v", rep.Summary.Reasons)
	}
}

// A proposed lesson is an unreviewed machine draft (ADR-0016's candidate lane) —
// not ground truth.
func TestProposedLessonExcluded(t *testing.T) {
	dir := t.TempDir()
	writeLesson(t, dir, "draft.md", lessonFile("LESSON-draft", "proposed", "", cleanBody))
	cases, rep, err := Export(Options{RepoDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 0 {
		t.Fatalf("a proposed lesson must not emit: %+v", numbers(cases))
	}
	if rep.Summary.Reasons[ReasonUnratified] != 1 {
		t.Errorf("reasons = %+v", rep.Summary.Reasons)
	}
}

// Decisions are not lessons and are not exported in v1 (ADR-0027).
func TestDecisionsNotExported(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".nugit", "decisions", "0001-x.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nschema_version: 1\nid: ADR-0001\ntype: decision\nscope: global\nstatus: accepted\n" +
		"created: 2026-07-01T00:00:00Z\nprovenance:\n  commit: seed\n---\n\n# ADR-0001 — x\n\n## Context\n\n" +
		"the exporter reported zero rows for three consecutive nightly runs with no error logged.\n\n" +
		"## Decision\n\nadvance the cursor after the commit returns.\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cases, rep, err := Export(Options{RepoDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 0 || rep.Summary.Lessons != 0 {
		t.Errorf("decisions leaked into the corpus: %+v / %+v", numbers(cases), rep.Summary)
	}
}

// The JSONL contract: one strict-JSON object per line, exactly the agreed fields,
// with embedded newlines/quotes escaped rather than breaking the line framing.
func TestJSONLShapeAndEscaping(t *testing.T) {
	c := Case{
		Number:     "lesson:x",
		Title:      `a "quoted" title`,
		Source:     "lessons/x.md",
		Input:      "line one\nline two\ttabbed & <angled>",
		GoldAnswer: "cause\n\nDo not: \"that\"",
		Labels:     []string{"lesson", "scope:delta", "tier:high-3"},
	}
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, []Case{c, c}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %d: %q", len(lines), buf.String())
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line is not valid JSON: %v (%q)", err, lines[0])
	}
	want := []string{"number", "title", "source", "input", "gold_answer", "labels"}
	if len(got) != len(want) {
		t.Errorf("field set = %v, want exactly %v", keys(got), want)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing field %q", k)
		}
	}
	if got["input"] != c.Input {
		t.Errorf("input did not round-trip: %q", got["input"])
	}
	if !strings.Contains(lines[0], "<angled>") {
		t.Errorf("HTML escaping should be off for readability: %q", lines[0])
	}
	if labels, ok := got["labels"].([]any); !ok || len(labels) != 3 {
		t.Errorf("labels = %v, want 3 entries", got["labels"])
	}
}

// An emitted case always carries labels (never a JSON null), including its scope
// and tier so the harness can break scores down per area and per confidence.
func TestLabels(t *testing.T) {
	dir := t.TempDir()
	writeLesson(t, dir, "a.md", lessonFile("LESSON-a", "active", "", cleanBody))
	cases, _, err := Export(Options{RepoDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 {
		t.Fatalf("want 1 case, got %d", len(cases))
	}
	got := strings.Join(cases[0].Labels, ",")
	if got != "lesson,scope:delta,tier:high-3" {
		t.Errorf("labels = %q", got)
	}
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, []Case{{Number: "n"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"labels":[]`) {
		t.Errorf("labels must serialize as [] not null: %s", buf.String())
	}
}

// -min-tier high-3 is the precision dial: thin-but-clean cases stop being
// emitted and start being reported.
func TestMinTierHigh3(t *testing.T) {
	dir := t.TempDir()
	thin := "# Lesson — the queue drained twice\n\n**Trigger:** the queue reported zero " +
		"pending jobs while producers kept writing.\n\n**Insight:** two workers shared one " +
		"lease id, so each acked the other's message. Give every worker its own lease id.\n"
	writeLesson(t, dir, "thin.md", lessonFile("LESSON-thin", "active", "", thin))

	cases, rep, err := Export(Options{RepoDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || rep.Summary.High2 != 1 {
		t.Fatalf("default tier should emit the thin case: %+v", rep.Summary)
	}
	cases, rep, err = Export(Options{RepoDir: dir, MinTier: "high-3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 0 || rep.Summary.Reasons[ReasonThinTrigger] != 1 {
		t.Errorf("-min-tier high-3 must refuse the thin case: %+v", rep.Summary)
	}
}

func TestUnknownMinTierErrors(t *testing.T) {
	if _, _, err := Export(Options{RepoDir: t.TempDir(), MinTier: "high-9"}); err == nil {
		t.Error("want an error for an unknown -min-tier")
	}
}

// The export is deterministic: same store in, byte-identical corpus out.
func TestExportIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeLesson(t, dir, "b.md", lessonFile("LESSON-b", "active", "", cleanBody))
	writeLesson(t, dir, "a.md", lessonFile("LESSON-a", "active", "", cleanBody))
	var first string
	for i := 0; i < 3; i++ {
		cases, _, err := Export(Options{RepoDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := WriteJSONL(&buf, cases); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = buf.String()
			continue
		}
		if buf.String() != first {
			t.Fatal("corpus is not byte-stable across runs")
		}
	}
	if !strings.HasPrefix(first, `{"number":"lesson:a"`) {
		t.Errorf("cases are not sorted by number: %q", first[:40])
	}
}

// A store with no .nugit at all is empty, not an error.
func TestEmptyStore(t *testing.T) {
	cases, rep, err := Export(Options{RepoDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 0 || rep.Summary.Lessons != 0 {
		t.Errorf("empty store = %+v / %+v", numbers(cases), rep.Summary)
	}
	if !strings.Contains(rep.SummaryLines(), "0 case(s) emitted") {
		t.Errorf("summary = %q", rep.SummaryLines())
	}
}

// titleOf drops the "Lesson — " lead-in so the metadata title reads as a claim.
func TestTitleOf(t *testing.T) {
	o := model.KnowledgeObject{FrontMatter: model.FrontMatter{ID: "LESSON-x"}, Body: "# Lesson — the reaper deletes tags\n"}
	if got := titleOf(o); got != "the reaper deletes tags" {
		t.Errorf("titleOf = %q", got)
	}
	o2 := model.KnowledgeObject{FrontMatter: model.FrontMatter{ID: "LESSON-y"}, Body: "no heading\n"}
	if got := titleOf(o2); got != "LESSON-y" {
		t.Errorf("titleOf fallback = %q", got)
	}
}

func numbers(cs []Case) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Number
	}
	return out
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
