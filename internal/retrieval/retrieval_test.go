package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func obj(id, typ, scope, body string, relates string) string {
	s := "---\nschema_version: 1\nid: " + id + "\ntype: " + typ + "\nscope: " + scope +
		"\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\n"
	if relates != "" {
		s += "relates_to:\n  - " + relates + "\n"
	}
	return s + "provenance:\n  commit: x\n---\n\n# " + id + " title\n\n" + body + "\n"
}

func setup(t *testing.T) string {
	dir := t.TempDir()
	wf(t, dir, ".nugit/architecture/workspace.dsl", `workspace "m" {
  model { sys = softwareSystem "m" {
    render = component "R" { properties { paths "internal/render/**" } }
    util   = component "U" { properties { paths "internal/util/**" } }
    render -> util
  } }
}`)
	wf(t, dir, ".nugit/decisions/r.md", obj("ADR-R", "decision", "render", "render decision", ""))
	wf(t, dir, ".nugit/decisions/g.md", obj("ADR-G", "decision", "global", "global decision", ""))
	wf(t, dir, ".nugit/decisions/u.md", obj("ADR-U", "decision", "util", "util decision", ""))
	wf(t, dir, ".nugit/lessons/l.md", obj("LESSON-L", "lesson", "render", "rendering lesson", "prevents:ADR-G"))
	return dir
}

func ids(items []Item) map[string]bool {
	m := map[string]bool{}
	for _, it := range items {
		m[it.ID] = true
	}
	return m
}

func TestContextScopingAndSlice(t *testing.T) {
	dir := setup(t)
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Component != "render" {
		t.Fatalf("component = %q, want render", b.Component)
	}
	if len(b.C4.DependsOn) != 1 || b.C4.DependsOn[0] != "util" {
		t.Errorf("c4 slice depends_on = %v, want [util]", b.C4.DependsOn)
	}
	d := ids(b.Decisions)
	if !d["ADR-R"] || !d["ADR-G"] {
		t.Errorf("render + global decisions must be present: %v", d)
	}
	if d["ADR-U"] {
		t.Error("util-scoped decision must NOT be in render's bundle (out of scope)")
	}
	if !ids(b.Lessons)["LESSON-L"] {
		t.Error("render-scoped lesson missing")
	}
}

func TestOneHopTraversal(t *testing.T) {
	dir := setup(t)
	// LESSON-L relates_to prevents:ADR-G — ADR-G is global so already in; make a
	// util-only target reachable only via the edge to prove traversal pulls it.
	wf(t, dir, ".nugit/decisions/x.md", obj("ADR-X", "decision", "other", "linked decision", ""))
	wf(t, dir, ".nugit/lessons/l.md", obj("LESSON-L", "lesson", "render", "lesson", "prevents:ADR-X"))
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	var viaX *Item
	for i := range b.Decisions {
		if b.Decisions[i].ID == "ADR-X" {
			viaX = &b.Decisions[i]
		}
	}
	if viaX == nil {
		t.Fatalf("ADR-X should be pulled in via one-hop relates_to; decisions=%v", ids(b.Decisions))
	}
	if !strings.Contains(viaX.Via, "LESSON-L") {
		t.Errorf("ADR-X.Via should name the linking object, got %q", viaX.Via)
	}
}

func TestBudgetTruncationSignals(t *testing.T) {
	dir := setup(t)
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go", BudgetTokens: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Truncated || len(b.Dropped) == 0 {
		t.Errorf("a tiny budget must truncate WITH a Dropped signal (no silent cut): %+v", b)
	}
	if b.EstimatedTokens > 30+50 {
		t.Errorf("estimated tokens %d wildly over budget 30", b.EstimatedTokens)
	}
}

func TestReferenceSelectionAndReverseInforms(t *testing.T) {
	dir := setup(t)
	// In-scope reference: selected like a lesson (keyword match or no-task).
	wf(t, dir, ".nugit/references/rfc.md",
		obj("REF-RFC", "reference", "render", "vendor doc: rendering pipeline flushes per frame", ""))
	// Out-of-scope reference that informs an in-scope decision: pulled by the
	// reverse informs: pass, not by scope.
	wf(t, dir, ".nugit/references/paper.md",
		obj("REF-PAPER", "reference", "util", "benchmark grounding ADR-R", "informs:ADR-R"))
	// Out-of-scope reference with no edge: must stay out.
	wf(t, dir, ".nugit/references/noise.md",
		obj("REF-NOISE", "reference", "util", "unrelated noise", ""))

	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	r := ids(b.References)
	if !r["REF-RFC"] {
		t.Errorf("in-scope reference missing: %v", r)
	}
	if !r["REF-PAPER"] {
		t.Errorf("reference informing an in-scope decision must be pulled: %v", r)
	}
	if r["REF-NOISE"] {
		t.Error("out-of-scope reference with no informs edge must NOT surface")
	}
	for _, it := range b.References {
		if it.ID == "REF-PAPER" && it.Via != "informs:ADR-R" {
			t.Errorf("REF-PAPER via = %q, want informs:ADR-R", it.Via)
		}
	}
}

func TestReferenceKeywordFilterWithTask(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/references/match.md",
		obj("REF-MATCH", "reference", "render", "notes about caching strategies", ""))
	wf(t, dir, ".nugit/references/miss.md",
		obj("REF-MISS", "reference", "render", "totally unrelated topic", ""))
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go", Task: "add caching"})
	if err != nil {
		t.Fatal(err)
	}
	r := ids(b.References)
	if !r["REF-MATCH"] || r["REF-MISS"] {
		t.Errorf("task keywords must filter references like lessons: %v", r)
	}
}

func TestSupersededReferenceNeverSurfaces(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/references/old.md",
		obj("REF-OLD", "reference", "util", "stale claims", "informs:ADR-R"))
	newer := strings.Replace(
		obj("REF-NEW", "reference", "util", "fresh claims", "informs:ADR-R"),
		"provenance:", "supersedes: REF-OLD\nprovenance:", 1)
	wf(t, dir, ".nugit/references/new.md", newer)
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	r := ids(b.References)
	if r["REF-OLD"] {
		t.Error("superseded reference must not surface as live context")
	}
	if !r["REF-NEW"] {
		t.Errorf("superseding reference should surface: %v", r)
	}
}

func TestTruncateDropsReferencesBeforeLessons(t *testing.T) {
	b := &Bundle{
		C4:        C4Slice{Component: "c"},
		Decisions: []Item{{ID: "D1", tokens: 30}},
		Lessons:   []Item{{ID: "L1", tokens: 30}},
		References: []Item{
			{ID: "R1", tokens: 30},
		},
	}
	// Budget fits baseline (~20+1) + decision + lesson, not the reference.
	truncate(b, 90)
	if len(b.Decisions) != 1 || len(b.Lessons) != 1 {
		t.Fatalf("decisions/lessons should survive: %d/%d", len(b.Decisions), len(b.Lessons))
	}
	if len(b.References) != 0 || !b.Truncated {
		t.Fatalf("reference should be dropped first: refs=%d truncated=%v", len(b.References), b.Truncated)
	}
	found := false
	for _, d := range b.Dropped {
		if strings.HasPrefix(d, "reference R1") {
			found = true
		}
	}
	if !found {
		t.Errorf("drop must be recorded, got %v", b.Dropped)
	}
}
