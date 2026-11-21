package editgate

import (
	"path/filepath"
	"strings"
	"testing"
)

// cloneOf clones origin into a fresh directory, so the clone records
// refs/remotes/origin/HEAD the way a real checkout does.
func cloneOf(t *testing.T, origin string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	gitDo(t, filepath.Dir(dir), "clone", "-q", origin, dir)
	return dir
}

// ADR-0055: an agent the gate denies must not be able to switch the gate off
// in the file next to the one it was editing. The mode comes from origin's
// default branch, so an edit to the checkout's config.yml changes nothing
// until it lands there.
func TestGateModeComesFromTheDefaultBranch(t *testing.T) {
	origin := repo(t, "block")
	dir := cloneOf(t, origin)
	write(t, dir, ".nugit/config.yml", "context_gate: off\n")

	o := run(t, dir, input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil)
	if o == nil || o.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("origin's context_gate: block governs, whatever the checkout says: %+v", o)
	}
	cfg, err := LocalConfig(dir)
	if err != nil || cfg.ContextGate != "block" {
		t.Fatalf("LocalConfig: want block from origin, got %q (%v)", cfg.ContextGate, err)
	}
	// Head keys still come from the checkout.
	write(t, dir, ".nugit/config.yml", "context_gate: off\nc4:\n  dsl: elsewhere.dsl\n")
	if cfg, _ := LocalConfig(dir); cfg.C4.DSL != "elsewhere.dsl" || cfg.ContextGate != "block" {
		t.Errorf("c4.dsl is the checkout's, context_gate is origin's: %+v", cfg)
	}
}

// A repo with no remote default has no branch the change cannot modify, so
// the checkout governs exactly as before ADR-0055.
func TestNoRemoteDefaultReadsTheCheckout(t *testing.T) {
	dir := repo(t, "block")
	write(t, dir, ".nugit/config.yml", "context_gate: off\n")
	if o := run(t, dir, input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil); o != nil {
		t.Fatalf("without origin the checkout's off governs: %+v", o)
	}
}

// Origin adopted nugit without a config.yml: the checkout's governs, as it
// would for an adoption PR at pr-render time.
func TestDefaultBranchWithoutConfigReadsTheCheckout(t *testing.T) {
	origin := repo(t, "")
	dir := cloneOf(t, origin)
	write(t, dir, ".nugit/config.yml", "context_gate: block\n")
	cfg, err := LocalConfig(dir)
	if err != nil || cfg.ContextGate != "block" {
		t.Fatalf("no config on origin: the checkout's block governs, got %q (%v)", cfg.ContextGate, err)
	}
	o := run(t, dir, input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil)
	if o == nil || !strings.Contains(o.HookSpecificOutput.PermissionDecisionReason, "block") {
		t.Fatalf("want a deny under the checkout's block: %+v", o)
	}
}
