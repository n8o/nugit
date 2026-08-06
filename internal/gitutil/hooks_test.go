package gitutil

import (
	"os"
	"path/filepath"
	"testing"
)

// nugitHook is the script `nugit init` writes (only the signature line matters).
const nugitHook = "#!/bin/sh\n" +
	"# installed by `nugit init` — validates the commit-trailer block (§6.1)\n" +
	"command -v nugit >/dev/null 2>&1 || exit 0\n" +
	"exec nugit hook commit-msg \"$1\"\n"

// commitlintHook stands in for the validator a hook-manager repo already has.
const commitlintHook = "#!/bin/sh\nnpx --no -- commitlint --edit \"$1\"\n"

// huskyShim stands in for a generated shim: it runs the same-named file in the
// parent, and it is NOT nugit's hook even in a repo where nugit is installed.
const huskyShim = "#!/usr/bin/env sh\n. \"$(dirname -- \"$0\")/h\"\n"

func writeExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// newRepo initializes a hermetic repo with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init")
	mustGit(t, dir, "commit", "--allow-empty", "-m", "root")
	return dir
}

// The plain layout — core.hooksPath unset — must keep behaving exactly as it
// did: the hook git runs is the hook, and the install target is the same file.
func TestCommitMsgHookPlainLayout(t *testing.T) {
	dir := newRepo(t)
	writeExec(t, filepath.Join(dir, ".git", "hooks", "commit-msg"), nugitHook)

	h := (Repo{Dir: dir}).CommitMsgHook()
	if !h.Installed() {
		t.Fatalf("plain .git/hooks install must be detected: %+v", h)
	}
	if h.Shimmed() {
		t.Errorf("plain layout must not look shimmed: Dirs=%v", h.Dirs)
	}
	if got, want := h.Path, filepath.Join(dir, ".git", "hooks", "commit-msg"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if h.Target != h.Path {
		t.Errorf("Target = %q, want it to equal Path %q", h.Target, h.Path)
	}
	if h.Inert != "" {
		t.Errorf("nothing is inert when git's default IS the hooks dir: %q", h.Inert)
	}
}

// THE REGRESSION. husky >= 9 sets core.hooksPath to a generated `_` directory
// that does not even exist in a fresh checkout (the package manager writes it),
// while the hook a developer commits lives one level up. Looking only where git
// points reported a correctly-installed hook as missing.
func TestCommitMsgHookShimDirLayout(t *testing.T) {
	dir := newRepo(t)
	mustGit(t, dir, "config", "core.hooksPath", ".husky/_")
	writeExec(t, filepath.Join(dir, ".husky", "commit-msg"), nugitHook)
	// .husky/_ deliberately absent: this is a fresh checkout, pre-install.

	h := (Repo{Dir: dir}).CommitMsgHook()
	if !h.Installed() {
		t.Fatalf("hook at .husky/commit-msg must be detected: %+v", h)
	}
	if !h.Shimmed() {
		t.Errorf("a `_` hooks dir must be recognized as generated: Dirs=%v", h.Dirs)
	}
	if got, want := h.Path, filepath.Join(dir, ".husky", "commit-msg"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if h.Target != h.Path {
		t.Errorf("init must target the developer-owned file: Target=%q, want %q", h.Target, h.Path)
	}
	// The generated dir must never be the install target — it is rewritten on
	// every package install and is typically gitignored.
	if filepath.Base(filepath.Dir(h.Target)) == "_" {
		t.Errorf("Target must not be inside the generated shim dir: %q", h.Target)
	}
}

// Once the package manager has generated the shims, the shim itself is a
// commit-msg hook that is not nugit's — it must not mask the real one.
func TestCommitMsgHookShimPresentDoesNotMaskTheRealHook(t *testing.T) {
	dir := newRepo(t)
	mustGit(t, dir, "config", "core.hooksPath", ".husky/_")
	writeExec(t, filepath.Join(dir, ".husky", "_", "commit-msg"), huskyShim)
	writeExec(t, filepath.Join(dir, ".husky", "commit-msg"), nugitHook)

	h := (Repo{Dir: dir}).CommitMsgHook()
	if !h.Installed() {
		t.Fatalf("the developer-owned hook must still win: %+v", h)
	}
	if got, want := h.Path, filepath.Join(dir, ".husky", "commit-msg"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if len(h.Foreign) != 1 || h.Foreign[0] != filepath.Join(dir, ".husky", "_", "commit-msg") {
		t.Errorf("the generated shim is a foreign hook, got Foreign=%v", h.Foreign)
	}
	if h.ForeignAtTarget() != "" {
		t.Errorf("the shim does not occupy the install target: %q", h.ForeignAtTarget())
	}
}

// The older layout points core.hooksPath at the developer-owned directory
// itself; there is no `_` child, so nothing is shimmed and the hook git runs is
// the hook a developer commits.
func TestCommitMsgHookOlderManagerLayout(t *testing.T) {
	dir := newRepo(t)
	mustGit(t, dir, "config", "core.hooksPath", ".husky")
	writeExec(t, filepath.Join(dir, ".husky", "commit-msg"), nugitHook)

	h := (Repo{Dir: dir}).CommitMsgHook()
	if !h.Installed() {
		t.Fatalf("hook at .husky/commit-msg must be detected: %+v", h)
	}
	if h.Shimmed() {
		t.Errorf("core.hooksPath=.husky is not a generated dir: Dirs=%v", h.Dirs)
	}
	if got, want := h.Path, filepath.Join(dir, ".husky", "commit-msg"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if h.Target != h.Path {
		t.Errorf("Target = %q, want %q", h.Target, h.Path)
	}
}

// A hooks directory that does not exist yet is simply empty — no crash, no
// panic, and a usable install target so `nugit init` can create it.
func TestCommitMsgHookMissingHooksDirDegrades(t *testing.T) {
	dir := newRepo(t)
	mustGit(t, dir, "config", "core.hooksPath", ".husky/_")
	// Neither .husky nor .husky/_ exists.

	h := (Repo{Dir: dir}).CommitMsgHook()
	if h.Installed() {
		t.Fatalf("nothing is installed: %+v", h)
	}
	if h.Shimmed() {
		t.Errorf("with no parent directory to see, there is no shim layout to claim: %v", h.Dirs)
	}
	if want := filepath.Join(dir, ".husky", "_", "commit-msg"); h.Target != want {
		t.Errorf("Target = %q, want the dir git points at, %q", h.Target, want)
	}

	// And also the plain flavour: git's own hooks dir removed.
	plain := newRepo(t)
	if err := os.RemoveAll(filepath.Join(plain, ".git", "hooks")); err != nil {
		t.Fatal(err)
	}
	p := (Repo{Dir: plain}).CommitMsgHook()
	if p.Installed() || p.Target == "" {
		t.Fatalf("a removed .git/hooks must degrade to not-installed with a target: %+v", p)
	}
}

// A commit-msg hook nugit did not write is reported, not mistaken for nugit's.
func TestCommitMsgHookForeignHookIsReported(t *testing.T) {
	dir := newRepo(t)
	mustGit(t, dir, "config", "core.hooksPath", ".husky/_")
	writeExec(t, filepath.Join(dir, ".husky", "commit-msg"), commitlintHook)

	h := (Repo{Dir: dir}).CommitMsgHook()
	if h.Installed() {
		t.Fatalf("a commitlint hook is not a nugit hook: %+v", h)
	}
	want := filepath.Join(dir, ".husky", "commit-msg")
	if h.ForeignAtTarget() != want {
		t.Errorf("ForeignAtTarget = %q, want %q", h.ForeignAtTarget(), want)
	}
}

// A repo that chains nugit into its own hook IS installed: the signature is the
// command, not the generated header.
func TestCommitMsgHookChainedIsInstalled(t *testing.T) {
	dir := newRepo(t)
	writeExec(t, filepath.Join(dir, ".git", "hooks", "commit-msg"),
		"#!/bin/sh\nnpx --no -- commitlint --edit \"$1\" || exit 1\nnugit hook commit-msg \"$1\"\n")

	h := (Repo{Dir: dir}).CommitMsgHook()
	if !h.Installed() {
		t.Fatalf("a hook that calls nugit is installed: %+v", h)
	}
	if len(h.Foreign) != 0 {
		t.Errorf("a chained hook is not foreign: %v", h.Foreign)
	}
}

// A nugit hook in git's DEFAULT hooks dir while core.hooksPath points elsewhere
// is installed but dead. Reporting it green would be exactly the class of lie
// this fix exists to remove — so it is not-installed, plus a precise reason.
func TestCommitMsgHookInertWhenHooksPathRedirects(t *testing.T) {
	dir := newRepo(t)
	mustGit(t, dir, "config", "core.hooksPath", "tools/githooks")
	if err := os.MkdirAll(filepath.Join(dir, "tools", "githooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(dir, ".git", "hooks", "commit-msg"), nugitHook)

	h := (Repo{Dir: dir}).CommitMsgHook()
	if h.Installed() {
		t.Fatalf("git never runs .git/hooks once core.hooksPath is set: %+v", h)
	}
	if h.Inert == "" {
		t.Error("the unreachable hook must be named, so the fix is obvious")
	}
	if want := filepath.Join(dir, "tools", "githooks", "commit-msg"); h.Target != want {
		t.Errorf("Target = %q, want %q", h.Target, want)
	}
}

// The `_` rule must not swallow the working-tree root: core.hooksPath=_ has no
// developer-owned parent to climb to, only the checkout itself.
func TestCommitMsgHookShimRuleStopsAtWorktreeRoot(t *testing.T) {
	dir := newRepo(t)
	mustGit(t, dir, "config", "core.hooksPath", "_")
	if err := os.MkdirAll(filepath.Join(dir, "_"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := (Repo{Dir: dir}).CommitMsgHook()
	if h.Shimmed() {
		t.Fatalf("the working-tree root is not a hooks directory: Dirs=%v", h.Dirs)
	}
	if want := filepath.Join(dir, "_", "commit-msg"); h.Target != want {
		t.Errorf("Target = %q, want %q", h.Target, want)
	}
}

// Outside a git repo everything degrades to the zero value: nothing found,
// nothing to install, no crash.
func TestCommitMsgHookNotARepo(t *testing.T) {
	h := (Repo{Dir: t.TempDir()}).CommitMsgHook()
	if h.Installed() || h.Target != "" || h.GitHooksDir != "" || len(h.Dirs) != 0 {
		t.Fatalf("non-repo must yield the zero value, got %+v", h)
	}
}

// The invariant that makes init and doctor agree: whatever the layout, the
// install target lives in a directory detection searches.
func TestCommitMsgHookTargetIsAlwaysSearched(t *testing.T) {
	layouts := []struct {
		name       string
		hooksPath  string
		makeDirs   []string
		wantSearch int
	}{
		{name: "plain", wantSearch: 1},
		{name: "shim dir", hooksPath: ".husky/_", makeDirs: []string{".husky"}, wantSearch: 2},
		{name: "shim dir, no parent", hooksPath: ".husky/_", wantSearch: 1},
		{name: "older manager", hooksPath: ".husky", makeDirs: []string{".husky"}, wantSearch: 1},
		{name: "custom dir", hooksPath: "tools/githooks", makeDirs: []string{"tools/githooks"}, wantSearch: 1},
	}
	for _, tc := range layouts {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			if tc.hooksPath != "" {
				mustGit(t, dir, "config", "core.hooksPath", tc.hooksPath)
			}
			for _, d := range tc.makeDirs {
				if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			h := (Repo{Dir: dir}).CommitMsgHook()
			if len(h.Dirs) != tc.wantSearch {
				t.Errorf("searched %v, want %d dir(s)", h.Dirs, tc.wantSearch)
			}
			if !sameDirAny(filepath.Dir(h.Target), h.Dirs) {
				t.Fatalf("Target %q is outside the search order %v — init and doctor could disagree", h.Target, h.Dirs)
			}
		})
	}
}
