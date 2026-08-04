package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mustGit runs git in dir with identity/signing pinned so the test is hermetic.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init")
	mustGit(t, dir, "commit", "--allow-empty", "-m", "root")
	mustGit(t, dir, "checkout", "-b", "feat/current-branch")

	r := Repo{Dir: dir}
	if got := r.CurrentBranch(); got != "feat/current-branch" {
		t.Fatalf("want feat/current-branch, got %q", got)
	}

	mustGit(t, dir, "checkout", "--detach")
	if got := r.CurrentBranch(); got != "HEAD" {
		t.Fatalf("detached HEAD: want the literal \"HEAD\", got %q", got)
	}
}

func TestCurrentBranchNotARepo(t *testing.T) {
	if got := (Repo{Dir: t.TempDir()}).CurrentBranch(); got != "" {
		t.Fatalf(`non-repo: want "", got %q`, got)
	}
}

func TestLogPath(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init")
	commitFile := func(rel, content string, msg ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		args := []string{"add", rel}
		mustGit(t, dir, args...)
		c := []string{"commit"}
		for _, m := range msg {
			c = append(c, "-m", m)
		}
		mustGit(t, dir, c...)
	}
	commitFile("a.txt", "one", "touch a first")
	commitFile("b.txt", "two", "touch b", "decision: pin the tag\nlearned: rc4 freezes the preview")
	commitFile("a.txt", "three", "touch a second")

	r := Repo{Dir: dir}
	got, err := r.LogPath("a.txt", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("a.txt: want 2 commits, got %d", len(got))
	}
	if got[0].Subject != "touch a second" || got[1].Subject != "touch a first" {
		t.Errorf("want newest first, got %q then %q", got[0].Subject, got[1].Subject)
	}

	one, err := r.LogPath("a.txt", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Subject != "touch a second" {
		t.Errorf("-n must bound the result: got %v", one)
	}

	b, err := r.LogPath("b.txt", 5, "90 days")
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 1 {
		t.Fatalf("b.txt: want 1 commit, got %d", len(b))
	}
	if !strings.Contains(b[0].Body, "decision: pin the tag") ||
		!strings.Contains(b[0].Body, "learned: rc4 freezes the preview") {
		t.Errorf("body must carry the trailer lines, got %q", b[0].Body)
	}

	if _, err := (Repo{Dir: t.TempDir()}).LogPath("a.txt", 5, ""); err == nil {
		t.Error("not a repo must return an error (callers degrade best-effort)")
	}
}

// resolve normalizes symlinked spellings (macOS t.TempDir is under /var ->
// /private/var) so paths git reports physically compare equal to ours.
func resolve(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", p, err)
	}
	return r
}

// A linked worktree must resolve its own root via WorktreeRoot but the MAIN
// checkout's root via CommonRoot — that split is what lets per-request MCP
// resolution read the right checkout while .nugit-local/ and .nugit/.cache/
// stay shared across all worktrees (ADR-0025).
func TestCommonRootAcrossWorktrees(t *testing.T) {
	tmp := t.TempDir()
	main := filepath.Join(tmp, "repo")
	wt := filepath.Join(tmp, "wt")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, main, "init")
	mustGit(t, main, "commit", "--allow-empty", "-m", "root")
	mustGit(t, main, "worktree", "add", wt, "-b", "feat/wt")

	wantRoot := resolve(t, main)
	for _, d := range []string{main, wt} {
		if got := resolve(t, (Repo{Dir: d}).CommonRoot()); got != wantRoot {
			t.Errorf("CommonRoot from %s: want %s, got %s", d, wantRoot, got)
		}
	}
	top, err := (Repo{Dir: wt}).WorktreeRoot()
	if err != nil {
		t.Fatalf("WorktreeRoot(wt): %v", err)
	}
	if got := resolve(t, top); got != resolve(t, wt) {
		t.Errorf("WorktreeRoot from wt: want %s, got %s", resolve(t, wt), got)
	}
	if got := (Repo{Dir: wt}).CurrentBranch(); got != "feat/wt" {
		t.Errorf("branch from wt: want feat/wt, got %q", got)
	}
	// The identity key both checkouts share.
	if a, b := (Repo{Dir: main}).CommonGitDir(), (Repo{Dir: wt}).CommonGitDir(); a == "" || a != b {
		t.Errorf("CommonGitDir must match across worktrees: main=%q wt=%q", a, b)
	}
}

func TestCommonRootFallsBackOutsideGit(t *testing.T) {
	dir := t.TempDir()
	if got := (Repo{Dir: dir}).CommonRoot(); got != dir {
		t.Fatalf("non-repo CommonRoot: want %s, got %s", dir, got)
	}
	if _, err := (Repo{Dir: dir}).WorktreeRoot(); err == nil {
		t.Fatal("non-repo WorktreeRoot: want error, got nil")
	}
	if got := (Repo{Dir: dir}).CommonGitDir(); got != "" {
		t.Fatalf(`non-repo CommonGitDir: want "", got %q`, got)
	}
}
