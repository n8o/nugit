package gitutil

import (
	"os/exec"
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
