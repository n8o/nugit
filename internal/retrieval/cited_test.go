package retrieval

import (
	"encoding/json"
	"strings"
	"testing"
)

func position(items []Item, id string) int {
	for i, it := range items {
		if it.ID == id {
			return i
		}
	}
	return -1
}

// A file that names a store id gets that object whatever its scope and
// whatever the task, marked cited, ranked above path-bound and
// component-scoped items (ADR-0053).
func TestCitedObjectsAreServedFirstWhateverScopeOrTask(t *testing.T) {
	dir := setup(t)
	// Path-bound to the render tree: today's strongest tier.
	wf(t, dir, ".nugit/decisions/zz.md",
		objPaths("ADR-ZZPIN", "decision", "global", "bound global decision", "internal/render/**"))
	// Scoped to ANOTHER component's lesson, and a global decision the task
	// does not match: neither would be served for render.go without the
	// citation.
	wf(t, dir, ".nugit/lessons/other.md", obj("LESSON-OTHER", "lesson", "util", "a util lesson", ""))
	wf(t, dir, "internal/render/render.go",
		"package render\n\n// Owned by ADR-U; see also LESSON-OTHER and (ADR-G).\nfunc R() {}\n")

	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go", Task: "zzz qqq"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ADR-U", "ADR-G"} {
		i := position(b.Decisions, id)
		if i < 0 {
			t.Fatalf("cited decision %s must be served: %v", id, ids(b.Decisions))
		}
		if !b.Decisions[i].Cited {
			t.Errorf("%s must be marked cited", id)
		}
		if p := position(b.Decisions, "ADR-ZZPIN"); p >= 0 && p < i {
			t.Errorf("cited %s (at %d) must rank above the path-bound ADR-ZZPIN (at %d)", id, i, p)
		}
	}
	if position(b.Decisions, "ADR-ZZPIN") < 0 {
		t.Error("a path-bound decision is still served alongside cited ones")
	}
	if i := position(b.Lessons, "LESSON-OTHER"); i < 0 || !b.Lessons[i].Cited {
		t.Errorf("a cited lesson scoped elsewhere must be served, marked cited: %v", ids(b.Lessons))
	}
	if md := b.Markdown(); !strings.Contains(md, "cited by this file") {
		t.Errorf("markdown must say why a cited item is there:\n%s", md)
	}
	js, _ := json.Marshal(b)
	if !strings.Contains(string(js), `"cited":true`) {
		t.Errorf("bundle JSON must carry cited: %s", js)
	}
}

// A citation is an exact, case-sensitive key: a longer identifier that merely
// contains an id does not cite it, but an id followed by a hyphenated suffix
// does.
func TestCitationMatchesWholeKeysOnly(t *testing.T) {
	dir := setup(t)
	wf(t, dir, "internal/render/a.go", "// ADR-Ux and xADR-U and adr-u are not citations\n")
	wf(t, dir, "internal/render/b.go", "// the ADR-U-style rule\n")

	a, err := Context(Options{RepoDir: dir, Path: "internal/render/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if position(a.Decisions, "ADR-U") >= 0 {
		t.Errorf("ADR-U must not be cited by a file that only contains longer identifiers: %v", ids(a.Decisions))
	}
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/b.go"})
	if err != nil {
		t.Fatal(err)
	}
	if i := position(b.Decisions, "ADR-U"); i < 0 || !b.Decisions[i].Cited {
		t.Errorf("ADR-U-style cites ADR-U: %v", ids(b.Decisions))
	}
}

// A directory, a missing file and a path outside the repo read no citations,
// and the bundle is exactly what it was before ADR-0053.
func TestNoCitationsWithoutAReadableFileInTheRepo(t *testing.T) {
	dir := setup(t)
	wf(t, dir, "internal/render/render.go", "// ADR-U\n")
	for _, p := range []string{"internal/render", "internal/render/missing.go", "../outside.go"} {
		b, err := Context(Options{RepoDir: dir, Path: p})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for _, it := range b.Decisions {
			if it.Cited {
				t.Errorf("%s: nothing can be cited without a file to read, got %s cited", p, it.ID)
			}
		}
		if position(b.Decisions, "ADR-U") >= 0 {
			t.Errorf("%s: util-scoped ADR-U must stay out of scope: %v", p, ids(b.Decisions))
		}
	}
	if got := readHead(dir, "../outside.go", 10); got != "" {
		t.Errorf("readHead must not leave the repo, read %q", got)
	}
}

// A cited record that is superseded is not served as live context, but the
// bundle names it and its successor: the file still points a reader at it.
func TestCitedSupersededRecordIsNamedNotServed(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/decisions/old.md", obj("ADR-OLD", "decision", "render", "the old rule", ""))
	wf(t, dir, ".nugit/decisions/new.md", "---\nschema_version: 1\nid: ADR-NEW\ntype: decision\nscope: render\n"+
		"status: accepted\nsupersedes: ADR-OLD\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# ADR-NEW\n\nthe new rule\n")
	wf(t, dir, "internal/render/render.go", "// per ADR-OLD\n")

	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	if position(b.Decisions, "ADR-OLD") >= 0 {
		t.Error("a superseded record is never served, cited or not")
	}
	if len(b.CitedDead) != 1 || b.CitedDead[0] != "ADR-OLD (superseded by ADR-NEW)" {
		t.Errorf("CitedDead = %v, want [ADR-OLD (superseded by ADR-NEW)]", b.CitedDead)
	}
	if md := b.Markdown(); !strings.Contains(md, "ADR-OLD (superseded by ADR-NEW)") {
		t.Errorf("the stale citation must be a notice:\n%s", md)
	}
}

func TestCitedIDsIsBoundedAndCheap(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", maxCitedScanBytes) + " ADR-LATE\n"
	wf(t, dir, "gen.txt", "ADR-EARLY "+big)
	got := citedIDs(dir, "gen.txt", map[string]bool{"ADR-EARLY": true, "ADR-LATE": true})
	if !got["ADR-EARLY"] || got["ADR-LATE"] {
		t.Errorf("only the first %d bytes are scanned: %v", maxCitedScanBytes, got)
	}
	if citedIDs(dir, "gen.txt", nil) != nil {
		t.Error("an empty store cites nothing")
	}
}
