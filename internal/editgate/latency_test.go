package editgate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/usage"
)

// hookBound is the bound the hooks read origin's config with. TestMain lifts
// it for every other test in this package; the tests below put it back where
// they need it.
var hookBound = usage.TargetReadTimeout

// Every test here asserts which config governs, not how fast git answered.
// Under load the default branch's read can take longer than the hooks' 500ms
// bound, the checkout's config then (correctly) applies, and a test that
// expected origin's value fails.
func TestMain(m *testing.M) {
	usage.TargetReadTimeout = time.Minute
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

// A git slower than the hooks' bound must not change which config the tests
// see. This fails while the package's tests run under the 500ms bound, which
// is how TestGateModeComesFromTheDefaultBranch and TestUsageLogAndGateAgree
// fail on a machine where git answers slowly.
func TestGovernedReadDoesNotDependOnGitLatency(t *testing.T) {
	dir := cloneOf(t, repo(t, "block"))
	write(t, dir, ".nugit/config.yml", "context_gate: off\n")
	slowGit(t, "0.6")

	if cfg, err := LocalConfig(dir); err != nil || cfg.ContextGate != "block" {
		t.Fatalf("origin's block must govern however long git takes under test, got %q (%v)", cfg.ContextGate, err)
	}
}

// The hooks' own bound still holds: past it the checkout's config applies,
// as before ADR-0055, rather than slow the edit.
func TestHookBoundFallsBackToTheCheckout(t *testing.T) {
	dir := cloneOf(t, repo(t, "block"))
	write(t, dir, ".nugit/config.yml", "context_gate: off\n")
	slowGit(t, "0.6")
	usage.TargetReadTimeout = hookBound
	t.Cleanup(func() { usage.TargetReadTimeout = time.Minute })

	if cfg, _ := LocalConfig(dir); cfg.ContextGate != "off" {
		t.Fatalf("a git slower than the %v bound must leave the checkout's off governing, got %q", hookBound, cfg.ContextGate)
	}
}
