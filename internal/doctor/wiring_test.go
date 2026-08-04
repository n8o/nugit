package doctor

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/config"
)

// findCheck returns the named check from a slice, or nil.
func findCheck(cs []Check, name string) *Check {
	for i := range cs {
		if cs[i].Name == name {
			return &cs[i]
		}
	}
	return nil
}

func cfgWith(t *testing.T, yml string) config.Config {
	t.Helper()
	c, err := config.LoadBytes([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// ADR-0026 (a): a workflow invoking a weaker -fail-on than config declares is
// flagged — this is exactly the pilot incident (config: fail, CI: none, both
// knobs inert with nothing firing).
func TestWiringWeakFailOnFlagged(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".github/workflows/ci.yml",
		"jobs:\n  view:\n    steps:\n      - run: nugit pr-render -base main -fail-on none\n")
	cfg := cfgWith(t, "pr_render:\n  fail_on: fail\n")

	c := findCheck(wiringChecks(dir, cfg), "CI fail-on matches config")
	if c == nil {
		t.Fatal("CI fail-on check missing")
	}
	if c.OK {
		t.Error("weaker workflow fail-on must be flagged")
	}
	if !c.Advisory {
		t.Error("wiring drift must be advisory, never gating")
	}
	for _, want := range []string{"ci.yml", "none", "fail"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail should mention %q, got %q", want, c.Detail)
		}
	}
}

// Both spellings must be seen: CLI (-fail-on X) and action input (fail-on: X).
func TestWiringActionInputStyleFlagged(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".github/workflows/view.yaml",
		"steps:\n  - uses: n8o/nugit@v0.3.0\n    with:\n      fail-on: warn\n")
	cfg := cfgWith(t, "pr_render:\n  fail_on: fail\n")

	c := findCheck(wiringChecks(dir, cfg), "CI fail-on matches config")
	if c == nil || c.OK {
		t.Fatalf("action-input style fail-on: warn under config fail must be flagged, got %+v", c)
	}
}

// A workflow at the same (or stronger) level is clean.
func TestWiringMatchingFailOnClean(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".github/workflows/ci.yml", "run: nugit pr-render -fail-on fail\n")
	cfg := cfgWith(t, "pr_render:\n  fail_on: fail\n")

	if c := findCheck(wiringChecks(dir, cfg), "CI fail-on matches config"); c == nil || !c.OK {
		t.Fatalf("matching fail-on must be clean, got %+v", c)
	}
}

// ADR-0026 (b): pins that disagree across CLAUDE.md / skills / workflows.
func TestWiringPinDriftFlagged(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "CLAUDE.md", "Install: go install github.com/n8o/nugit/cmd/nugit@main\n")
	write(t, dir, ".claude/skills/nugit/SKILL.md", "Run nugit@main before edits.\n")
	write(t, dir, ".github/workflows/ci.yml", "uses: n8o/nugit@v0.3.0\n")

	c := findCheck(wiringChecks(dir, config.Default()), "install pins agree")
	if c == nil {
		t.Fatal("install pins check missing")
	}
	if c.OK {
		t.Error("disagreeing pins must be flagged")
	}
	if !c.Advisory {
		t.Error("pin drift must be advisory")
	}
	for _, want := range []string{"@main", "@v0.3.0", "CLAUDE.md", "ci.yml"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail should mention %q, got %q", want, c.Detail)
		}
	}
}

func TestWiringPinsAgreeClean(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "CLAUDE.md", "go install github.com/n8o/nugit/cmd/nugit@v0.3.0\n")
	write(t, dir, ".github/workflows/ci.yml", "uses: n8o/nugit@v0.3.0\n")

	if c := findCheck(wiringChecks(dir, config.Default()), "install pins agree"); c == nil || !c.OK {
		t.Fatalf("agreeing pins must be clean, got %+v", c)
	}
}

// ADR-0026 (c): skill prose asserting a stale c4.mode contradicting config —
// the pilot's SKILL.md still claimed warn after ratification to enforce.
func TestWiringSkillModeContradictionFlagged(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".claude/skills/nugit/SKILL.md", "This repo runs c4.mode: warn during adoption.\n")
	cfg := cfgWith(t, "c4:\n  mode: enforce\n")

	c := findCheck(wiringChecks(dir, cfg), "skill docs match config")
	if c == nil {
		t.Fatal("skill docs check missing")
	}
	if c.OK {
		t.Error("contradicting c4.mode claim must be flagged")
	}
	if !c.Advisory {
		t.Error("skill contradiction must be advisory")
	}
	for _, want := range []string{"SKILL.md", "warn", "enforce"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail should mention %q, got %q", want, c.Detail)
		}
	}
}

func TestWiringSkillModeMatchClean(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".claude/skills/nugit/SKILL.md", "This repo runs c4.mode: enforce.\n")
	cfg := cfgWith(t, "c4:\n  mode: enforce\n")

	if c := findCheck(wiringChecks(dir, cfg), "skill docs match config"); c == nil || !c.OK {
		t.Fatalf("matching c4.mode claim must be clean, got %+v", c)
	}
}

// Tolerance: an empty repo (no workflows, no CLAUDE.md, no skills) and files
// that are not valid YAML never break the scan — all checks present, all OK.
func TestWiringTolerantOnMissingAndGarbage(t *testing.T) {
	empty := t.TempDir()
	for _, name := range []string{"CI fail-on matches config", "install pins agree", "skill docs match config"} {
		c := findCheck(wiringChecks(empty, config.Default()), name)
		if c == nil || !c.OK || !c.Advisory {
			t.Fatalf("empty repo: %s must be present, OK, advisory; got %+v", name, c)
		}
	}

	garbage := t.TempDir()
	write(t, garbage, ".github/workflows/broken.yml", "\x00\x01{{ not yaml ::: fail-on\n")
	write(t, garbage, ".claude/skills/x/SKILL.md", "")
	for _, c := range wiringChecks(garbage, config.Default()) {
		if !c.OK || !c.Advisory {
			t.Fatalf("garbage files must never fail the scan: %+v", c)
		}
	}
}

// The wiring checks surface through doctor.Run and stay advisory there too.
func TestWiringChecksInRunAreAdvisory(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/config.yml", "schema_version: 1\npr_render:\n  fail_on: fail\n")
	write(t, dir, ".github/workflows/ci.yml", "run: nugit pr-render -fail-on none\n")

	rep := Run(dir)
	c := findCheck(rep.Checks, "CI fail-on matches config")
	if c == nil {
		t.Fatal("wiring check missing from doctor report")
	}
	if c.OK {
		t.Error("weak CI fail-on must be flagged in Run")
	}
	if !c.Advisory {
		t.Error("wiring check must never gate the doctor exit code")
	}
}
