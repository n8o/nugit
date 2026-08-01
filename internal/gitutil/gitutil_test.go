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
