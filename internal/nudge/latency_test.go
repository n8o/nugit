package nudge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// hookBound is the bound the commit-msg hook runs with. TestMain lifts
// gitTimeout for every other test in this package; the tests below put it back
// where they need it.
var hookBound = gitTimeout

// Every test here asserts what the nudge says, not how fast git said it. Under
// load a staged-diff read can take longer than the hook's 100ms bound, the
// nudge is then (correctly) skipped, and a test that expected one fails.
func TestMain(m *testing.M) {
	gitTimeout = time.Minute
	os.Exit(m.Run())
}

// slowGit puts a git on PATH that answers no sooner than delay. sleep never
// returns early, so a delay above a bound exceeds it on every run.
func slowGit(t *testing.T, delay string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nsleep " + delay + "\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A git slower than the hook's bound must not change what the tests see. This
// fails while the package's tests run under the hook's 100ms bound, which is
// how TestKeywordsSeededFromModelComponents failed on a loaded machine.
func TestNudgeDoesNotDependOnGitLatency(t *testing.T) {
	dir := repoWithBase(t)
	stageSignificant(t, dir)
	slowGit(t, "0.15")

	if out := ForStagedCommit(dir, "feat(alpha): add alpha and beta\n", nudgeCfg()); out == "" {
		t.Fatal("a significant staged diff must nudge however long git takes under test")
	}
}

// The hook's own bound still holds: a git slower than it skips the nudge
// rather than slow the commit.
func TestHookBoundSkipsTheNudge(t *testing.T) {
	dir := repoWithBase(t)
	stageSignificant(t, dir)
	slowGit(t, "0.15")
	gitTimeout = hookBound
	t.Cleanup(func() { gitTimeout = time.Minute })

	if out := ForStagedCommit(dir, "feat(alpha): add alpha and beta\n", nudgeCfg()); out != "" {
		t.Fatalf("a git slower than the %v hook bound must skip the nudge, got:\n%s", hookBound, out)
	}
}
