package localmem

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitDo runs git in dir with identity/signing pinned so the test is hermetic.
func gitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// All worktrees of one repo share ONE journal at the main checkout — a note
// jotted by an agent in a worktree is visible to every other agent, and no
// per-worktree .nugit-local/ silo is ever created (ADR-0025).
func TestJournalSharedAcrossWorktrees(t *testing.T) {
	tmp := t.TempDir()
	main := filepath.Join(tmp, "repo")
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDo(t, main, "init")
	gitDo(t, main, "commit", "--allow-empty", "-m", "root")
	gitDo(t, main, "worktree", "add", wt, "-b", "feat/wt")

	if err := Append(wt, Entry{Text: "from-worktree"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(main, Entry{Text: "from-main"}); err != nil {
		t.Fatal(err)
	}
	// Both checkouts see both notes, newest first, from either side.
	for _, d := range []string{main, wt} {
		got := Recent(d, 10)
		if len(got) != 2 || got[0].Text != "from-main" || got[1].Text != "from-worktree" {
			t.Fatalf("Recent(%s): want [from-main from-worktree], got %+v", d, got)
		}
	}
	// The journal physically lives at the main checkout only.
	if _, err := os.Stat(filepath.Join(main, dir, journal)); err != nil {
		t.Errorf("journal missing at the main checkout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, dir)); !os.IsNotExist(err) {
		t.Errorf("worktree grew its own %s silo (err=%v)", dir, err)
	}
}

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
