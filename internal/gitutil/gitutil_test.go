package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TrackedFiles reports the index, not the working tree: untracked and ignored
// paths are absent, and paths are relative to Dir (so a nugit root nested in a
// bigger repo gets its own coordinates, not git-root ones).
func TestTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	writeF := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, dir, "init")
	writeF(".gitignore", "ignored/\n")
	writeF("a.txt", "a")
	writeF("sub/b with space.txt", "b")
	writeF("ignored/c.txt", "c")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "tracked")
	writeF("untracked.txt", "u")

	got, err := (Repo{Dir: dir}).TrackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".gitignore", "a.txt", "sub/b with space.txt"} {
		if !got[want] {
			t.Errorf("%q should be tracked, got %v", want, got)
		}
	}
	for _, no := range []string{"untracked.txt", "ignored/c.txt"} {
		if got[no] {
			t.Errorf("%q must not be reported as tracked", no)
		}
	}
	// From a subdirectory: only that subtree, in subtree-relative coordinates.
	sub, err := (Repo{Dir: filepath.Join(dir, "sub")}).TrackedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 1 || !sub["b with space.txt"] {
		t.Errorf("subdir TrackedFiles: want [b with space.txt], got %v", sub)
	}
}

// Outside a git repo the error must surface, so callers can degrade instead of
// reading an empty set as "nothing is tracked".
func TestTrackedFilesNotARepo(t *testing.T) {
	if _, err := (Repo{Dir: t.TempDir()}).TrackedFiles(); err == nil {
		t.Fatal("non-repo TrackedFiles: want error, got nil")
	}
}

// mustGitAt is mustGit with the commit date pinned (window-boundary tests).
func mustGitAt(t *testing.T, dir, date string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestLogSince(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGit(t, dir, "add", "-A")
	}
	old := time.Now().AddDate(0, 0, -120).Format(time.RFC3339)
	write("a/a.go", "package a // v1\n")
	mustGitAt(t, dir, old, "commit", "-m", "fix(a): ancient — outside the window")
	write("a/a.go", "package a // v2\n")
	mustGit(t, dir, "commit", "-m", "fix(a): recent\n\nlearned: something\nkeywords: a")
	write("b/b.go", "package b\n")
	mustGit(t, dir, "commit", "-m", "fix(b): other component")

	r := Repo{Dir: dir}

	got, err := r.LogSince("HEAD", 90, 200, "a/**")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Subject != "fix(a): recent" {
		t.Fatalf("since+pathspec filter: got %+v", got)
	}
	if got[0].Body == "" {
		t.Error("LogSince must return full bodies (trailer parsing needs them)")
	}

	// No pathspec: both in-window commits, newest first.
	got, err = r.LogSince("HEAD", 90, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Subject != "fix(b): other component" {
		t.Fatalf("unfiltered: got %+v", got)
	}

	// max-count bounds the scan.
	got, err = r.LogSince("HEAD", 90, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("max-count: got %d commits", len(got))
	}
}

func TestNumstatCached(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "root")

	// clean index: empty map, no error
	counts, err := (Repo{Dir: dir}).NumstatCached(5 * time.Second)
	if err != nil || len(counts) != 0 {
		t.Fatalf("clean index: want empty, got %v err=%v", counts, err)
	}

	// staged change appears; unstaged change does not
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("unstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "a.txt") // b.txt stays unstaged (untracked)
	counts, err = (Repo{Dir: dir}).NumstatCached(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := counts["a.txt"]; !ok || got != [2]int{2, 0} {
		t.Errorf("a.txt: want [2 0], got %v (ok=%v)", got, ok)
	}
	if _, ok := counts["b.txt"]; ok {
		t.Error("unstaged b.txt must not appear in the cached diff")
	}
}

func TestNumstatCachedErrors(t *testing.T) {
	// not a git repo: error, never a silent empty result
	if _, err := (Repo{Dir: t.TempDir()}).NumstatCached(5 * time.Second); err == nil {
		t.Error("non-repo must return an error")
	}
	// expired timeout: bounded — returns an error instead of stalling the commit
	dir := t.TempDir()
	mustGit(t, dir, "init")
	if _, err := (Repo{Dir: dir}).NumstatCached(time.Nanosecond); err == nil {
		t.Error("an already-expired timeout must surface as an error")
	}
}

// DeletedPaths is the corroboration ADR-0038 gives `nugit adopt`: a path this
// repository once tracked and later removed is a fact the index already knows,
// and a path it never tracked is not this repository's to claim.
func TestDeletedPaths(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init")
	writeFile := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("apps/lathe/main.go", "package main\n")
	writeFile("apps/anvil/main.go", "package main\n")
	writeFile("libs/press/press.go", "package press\n")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "seed")

	if err := os.RemoveAll(filepath.Join(dir, "apps", "lathe")); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "retire the milling service")

	// A rename away is a deletion too: the question is whether this repository
	// ever contained the path, and a rename answers yes.
	mustGit(t, dir, "mv", "libs/press", "libs/presser")
	mustGit(t, dir, "commit", "-m", "rename the press library")

	r := Repo{Dir: dir}
	got, err := r.DeletedPaths([]string{
		"apps/lathe", "apps/anvil", "libs/press", "apps/never-existed",
	}, 500)
	if err != nil {
		t.Fatalf("DeletedPaths: %v", err)
	}
	del, ok := got["apps/lathe"]
	if !ok {
		t.Fatalf("apps/lathe was tracked and removed; got %+v", got)
	}
	if del.SHA == "" || del.Date == "" || del.File != "apps/lathe/main.go" {
		t.Errorf("a deletion must be citeable: %+v", del)
	}
	if _, ok := got["libs/press"]; !ok {
		t.Errorf("a renamed-away path must report as deleted: %+v", got)
	}
	if _, ok := got["apps/anvil"]; ok {
		t.Errorf("apps/anvil still exists and must not report a deletion: %+v", got)
	}
	if _, ok := got["apps/never-existed"]; ok {
		t.Errorf("a path this repo never tracked must be absent from the result: %+v", got)
	}
}

// The batch takes its input from prose, so it must refuse anything that would
// escape the repo or be read as pathspec magic — and must never error on it.
func TestDeletedPathsRejectsUnsafeSpecs(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init")
	mustGit(t, dir, "commit", "--allow-empty", "-m", "root")
	got, err := (Repo{Dir: dir}).DeletedPaths([]string{
		"../outside", ":(top)apps", "/etc/passwd", "", "   ",
	}, 100)
	if err != nil {
		t.Fatalf("unsafe specs must be dropped, not errored: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

// No history readable is an ERROR, never "nothing was ever deleted": a caller
// must be able to tell the two apart before it asserts a phantom.
func TestDeletedPathsOutsideAGitRepo(t *testing.T) {
	if _, err := (Repo{Dir: t.TempDir()}).DeletedPaths([]string{"apps/lathe"}, 100); err == nil {
		t.Error("want an error outside a git repo")
	}
}
