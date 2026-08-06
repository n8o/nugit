package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/scaffold"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}
	if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// hookCheck returns the gating commit-msg check from a doctor run.
func hookCheck(t *testing.T, repoDir string) Check {
	t.Helper()
	for _, c := range Run(repoDir).Checks {
		if c.Name == "commit-msg hook installed" {
			return c
		}
	}
	t.Fatal("doctor no longer reports a commit-msg hook check")
	return Check{}
}

// initRepo builds a hermetic repo in the given hook layout, with a source file
// so `nugit init` has something to model.
func initRepo(t *testing.T, hooksPath string, dirs ...string) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "index.ts"), []byte("export const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if hooksPath != "" {
		gitIn(t, dir, "config", "core.hooksPath", hooksPath)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "root")
	return dir
}

// THE INVARIANT: whatever the hook layout, `nugit init` must install where
// `nugit doctor` looks. A repo must not be able to reach a state where init
// writes a hook that doctor then calls missing — which is exactly what a
// core.hooksPath repo could reach before this fix.
func TestInitDoctorRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		hooksPath string
		dirs      []string
		wantHook  string // repo-relative path init must write
	}{
		{name: "plain .git/hooks", wantHook: ".git/hooks/commit-msg"},
		{name: "generated shim dir", hooksPath: ".husky/_", dirs: []string{".husky"}, wantHook: ".husky/commit-msg"},
		{name: "older manager layout", hooksPath: ".husky", dirs: []string{".husky"}, wantHook: ".husky/commit-msg"},
		{name: "custom hooks dir", hooksPath: "tools/githooks", dirs: []string{"tools/githooks"}, wantHook: "tools/githooks/commit-msg"},
		{name: "hooks dir not created yet", hooksPath: "tools/githooks", wantHook: "tools/githooks/commit-msg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := initRepo(t, tc.hooksPath, tc.dirs...)

			res, err := scaffold.Run(scaffold.Options{RepoDir: dir})
			if err != nil {
				t.Fatalf("nugit init: %v", err)
			}
			if !res.HookInstalled {
				t.Fatalf("init installed no hook (created=%v skipped=%v)", res.Created, res.Skipped)
			}
			want := filepath.Join(dir, filepath.FromSlash(tc.wantHook))
			if _, serr := os.Stat(want); serr != nil {
				t.Fatalf("init must write %s: %v", tc.wantHook, serr)
			}

			c := hookCheck(t, dir)
			if !c.OK {
				t.Fatalf("doctor disagrees with init: %s", c.Detail)
			}
			if !strings.Contains(c.Detail, tc.wantHook) {
				t.Errorf("detail must name where the hook was found, got %q", c.Detail)
			}

			// Idempotent: a second init changes nothing and doctor still passes.
			res2, err := scaffold.Run(scaffold.Options{RepoDir: dir})
			if err != nil {
				t.Fatalf("second init: %v", err)
			}
			if !contains(res2.Skipped, want) {
				t.Errorf("re-running init must skip the existing hook, skipped=%v", res2.Skipped)
			}
			if c := hookCheck(t, dir); !c.OK {
				t.Errorf("doctor after re-init: %s", c.Detail)
			}
		})
	}
}

// The regression, end to end: a hook committed at .husky/commit-msg in a repo
// whose core.hooksPath is the generated `_` dir — which does not exist until the
// package manager runs — used to make doctor exit 1 forever.
func TestDoctorFindsHookUnderGeneratedShimDir(t *testing.T) {
	dir := initRepo(t, ".husky/_", ".husky")
	if err := os.WriteFile(filepath.Join(dir, ".husky", "commit-msg"),
		[]byte("#!/bin/sh\nexec nugit hook commit-msg \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".husky", "_")); !os.IsNotExist(err) {
		t.Fatalf(".husky/_ must be absent for this test (fresh checkout), got %v", err)
	}

	c := hookCheck(t, dir)
	if !c.OK {
		t.Fatalf("a committed .husky/commit-msg is installed; doctor said: %s", c.Detail)
	}
	if !strings.Contains(c.Detail, ".husky/commit-msg") {
		t.Errorf("detail must say where it was found, got %q", c.Detail)
	}
	if !strings.Contains(c.Detail, ".husky/_") {
		t.Errorf("detail must name the convention in play, got %q", c.Detail)
	}
}

// A commit-msg hook nugit did not write is another tool's contract. init leaves
// it alone and names it; doctor reports honestly that nugit is not installed and
// says how to chain in — never a silent overwrite, and never a silent green.
func TestInitNeverClobbersAForeignHook(t *testing.T) {
	dir := initRepo(t, ".husky/_", ".husky")
	foreign := filepath.Join(dir, ".husky", "commit-msg")
	const body = "#!/bin/sh\nnpx --no -- commitlint --edit \"$1\"\n"
	if err := os.WriteFile(foreign, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := scaffold.Run(scaffold.Options{RepoDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if res.HookInstalled {
		t.Error("init must not install over a hook it did not write")
	}
	if res.ForeignHook != foreign {
		t.Errorf("ForeignHook = %q, want %q", res.ForeignHook, foreign)
	}
	if b, _ := os.ReadFile(foreign); string(b) != body {
		t.Fatalf("the foreign hook was modified: %q", b)
	}

	// -force governs the files nugit authors, not somebody else's commit contract.
	if _, err := scaffold.Run(scaffold.Options{RepoDir: dir, Force: true}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(foreign); string(b) != body {
		t.Fatalf("-force clobbered the foreign hook: %q", b)
	}

	c := hookCheck(t, dir)
	if c.OK {
		t.Error("nugit's hook is genuinely absent — doctor must not report it installed")
	}
	if !strings.Contains(c.Detail, ".husky/commit-msg") || !strings.Contains(c.Detail, "nugit hook commit-msg") {
		t.Errorf("detail must name the blocking hook and how to chain in, got %q", c.Detail)
	}
}

// -force refreshes a hook that IS nugit's (e.g. after the script changes).
func TestInitForceRefreshesItsOwnHook(t *testing.T) {
	dir := initRepo(t, ".husky/_", ".husky")
	stale := filepath.Join(dir, ".husky", "commit-msg")
	if err := os.WriteFile(stale, []byte("#!/bin/sh\n# old\nnugit hook commit-msg \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := scaffold.Run(scaffold.Options{RepoDir: dir, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.HookInstalled {
		t.Fatalf("-force must rewrite nugit's own hook: %+v", res)
	}
	b, _ := os.ReadFile(stale)
	if !strings.Contains(string(b), "installed by `nugit init`") {
		t.Errorf("hook was not refreshed: %q", b)
	}
	if c := hookCheck(t, dir); !c.OK {
		t.Errorf("doctor after -force: %s", c.Detail)
	}
}

// A repo with no git at all must degrade, not crash: the check simply fails
// with a detail that says why.
func TestDoctorHookCheckOutsideGit(t *testing.T) {
	c := hookCheck(t, t.TempDir())
	if c.OK {
		t.Fatal("no git repo, no hook")
	}
	if !strings.Contains(c.Detail, "not a git repo") {
		t.Errorf("detail should explain the degradation, got %q", c.Detail)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
