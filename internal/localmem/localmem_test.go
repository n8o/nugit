package localmem

import (
	"strings"
	"testing"
)

// A single oversized note must not wipe the whole store (bufio.Scanner ErrTooLong).
func TestRecentSurvivesOversizedLine(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Entry{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(dir, Entry{Text: strings.Repeat("x", 5*1024*1024)}); err != nil { // > old 4MB cap
		t.Fatal(err)
	}
	if err := Append(dir, Entry{Text: "third"}); err != nil {
		t.Fatal(err)
	}
	got := Recent(dir, 10)
	if len(got) != 3 {
		t.Fatalf("oversized line dropped entries: got %d, want 3", len(got))
	}
	if got[0].Text != "third" {
		t.Errorf("newest-first broken after big line: %q", got[0].Text)
	}
}

func TestAppendAndRecentNewestFirst(t *testing.T) {
	dir := t.TempDir()
	if got := Recent(dir, 10); got != nil {
		t.Errorf("empty store should yield nil, got %v", got)
	}
	for _, txt := range []string{"first", "second", "third"} {
		if err := Append(dir, Entry{Text: txt, Scope: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got := Recent(dir, 10)
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d", len(got))
	}
	if got[0].Text != "third" || got[2].Text != "first" {
		t.Errorf("not newest-first: %v", []string{got[0].Text, got[1].Text, got[2].Text})
	}
	if got[0].Kind != "note" || got[0].Time == "" {
		t.Errorf("defaults not applied: %+v", got[0])
	}
	if len(Recent(dir, 2)) != 2 {
		t.Error("max not honored")
	}
}
