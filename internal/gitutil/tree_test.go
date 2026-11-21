package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// ListTree is git-root-relative from a subdirectory too. Without --full-tree
// git lists only the subtree, relative to it, and every `prefix + ".nugit/"`
// filter in a nested root matched nothing.
func TestListTreeFromANestedRootIsGitRootRelative(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	for _, p := range []string{"README.md", "apps/op/.nugit/lessons/a.md"} {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		_ = os.WriteFile(filepath.Join(root, p), []byte("x\n"), 0o644)
	}
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "x")
	paths, err := Repo{Dir: filepath.Join(root, "apps", "op")}.ListTree("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(paths, ",")
	if got != "README.md,apps/op/.nugit/lessons/a.md" {
		t.Errorf("ListTree from apps/op = %s", got)
	}
}

// RawDiff's output is parsed, so a user's color.ui=always must not reach it.
func TestRawDiffIgnoresColorConfig(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "color.ui", "always")
	_ = os.WriteFile(filepath.Join(dir, "é.sql"), []byte("a\n"), 0o644)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "a")
	_ = os.WriteFile(filepath.Join(dir, "é.sql"), []byte("a\nALTER TABLE x\n"), 0o644)
	gitRun(t, dir, "commit", "-q", "-am", "b")
	out, err := Repo{Dir: dir}.RawDiff("HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b[") || !strings.Contains(out, "+++ b/é.sql") || !strings.Contains(out, "\n+ALTER TABLE x") {
		t.Errorf("diff must be plain and unquoted:\n%q", out)
	}
}
