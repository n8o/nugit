package orphans

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func publish(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "update-ref", "refs/remotes/origin/main", "main")
}

func TestGather(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, ".beads/issues.jsonl", `{"id":"a-1","title":"t","status":"in_progress"}`+"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	publish(t, dir)

	// A no-base clone is an error, never a guess.
	if _, err := Gather(dir, Options{NoSessions: true, NoGitHub: true}); !errors.Is(err, ErrNoBase) {
		t.Fatalf("err = %v, want ErrNoBase", err)
	}
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	// work: an objective's unmerged commit, checked out in a worktree.
	wt := filepath.Join(t.TempDir(), "work")
	git(t, dir, "worktree", "add", "-q", "-b", "work", wt)
	write(t, wt, "w.txt", "w\n")
	git(t, wt, "add", "-A")
	git(t, wt, "commit", "-q", "-m", "work", "-m", "objective: a-1")

	// squashed: its content landed on main as a different commit.
	git(t, dir, "checkout", "-q", "-b", "squashed")
	write(t, dir, "s.txt", "s\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "s1")
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "merge", "-q", "--squash", "squashed")
	git(t, dir, "commit", "-q", "-m", "squash of s1", "-m", "objective: a-2")
	publish(t, dir)

	in, err := Gather(dir, Options{NoSessions: true, NoGitHub: true, Stale: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if in.Default != "origin/main" || len(in.Store.Issues) != 1 || in.Sessions != nil || in.GitHub {
		t.Errorf("input = default %q, %d issues, sessions %v, github %v", in.Default, len(in.Store.Issues), in.Sessions, in.GitHub)
	}
	by := map[string]Branch{}
	for _, b := range in.Branches {
		by[b.Name] = b
	}
	if _, ok := by["main"]; ok {
		t.Error("the default branch is listed")
	}
	if w := by["work"]; w.Ahead != 1 || w.ContentMerged || w.Objective != "a-1" || w.Worktree == "" {
		t.Errorf("work = %+v", w)
	}
	if s := by["squashed"]; s.Ahead != 1 || !s.ContentMerged {
		t.Errorf("squashed = %+v, want its content detected on main", s)
	}
	if in.Named["a-1"].IsZero() || in.Named["a-2"].IsZero() {
		t.Errorf("named = %v, want a-1 (branch) and a-2 (main)", in.Named)
	}
}
