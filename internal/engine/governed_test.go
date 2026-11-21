package engine

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// Found 2026-11-21: the engine read config.yml at the PR's head, so a PR that
// added an undeclared edge could also write `c4.mode: warn` and pass. The
// target's mode now governs, and the policy change is its own finding.
func TestAPRCannotSwitchItsOwnGateOff(t *testing.T) {
	dir, _ := seed(t, false)
	write(t, dir, ".nugit/config.yml", "c4:\n  mode: enforce\npr_render:\n  fail_on: fail\n")
	target := commitAll(t, dir, "policy: enforce")

	git(t, dir, "checkout", "-q", "-b", "sneaky")
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\nfunc A() {}\n")
	write(t, dir, ".nugit/config.yml", "c4:\n  mode: warn\npr_render:\n  fail_on: none\n")
	head := commitAll(t, dir, "add an edge and relax the gate")

	rep, err := BuildReport(Options{RepoDir: dir, Base: target, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if hasC4Fail(rep) == nil {
		t.Fatalf("the undeclared edge must fail under the TARGET's c4.mode: enforce: %+v", rep.Findings)
	}
	if rep.FailOn != "fail" {
		t.Errorf("the governing fail_on is the target's: got %q", rep.FailOn)
	}
	fs := findings(rep, "enforcement-config")
	if len(fs) != 1 {
		t.Fatalf("want one enforcement-config finding, got %+v", rep.Findings)
	}
	if fs[0].Severity != model.SevWarn {
		t.Errorf("a policy change is reviewed, not failed: %s", fs[0].Severity)
	}
	for _, want := range []string{"`c4.mode`: `enforce` → `warn`", "`pr_render.fail_on`: `fail` → `none`"} {
		if !strings.Contains(fs[0].Detail, want) {
			t.Errorf("detail must name %q: %s", want, fs[0].Detail)
		}
	}

	// The same PR rendered against itself as target (what every render did
	// before) is judged by its own relaxed config. Proves the target is what
	// changed the verdict.
	rep2, err := BuildReport(Options{RepoDir: dir, Base: head, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if hasC4Fail(rep2) != nil || rep2.FailOn != "none" {
		t.Errorf("against itself the PR's own config governs; got fail_on=%s", rep2.FailOn)
	}
}

// The DSL's location describes the tree under review, so a PR that moves the
// model is checked against the model where the PR put it.
func TestAPRMayMoveItsModel(t *testing.T) {
	dir, _ := seed(t, true)
	write(t, dir, ".nugit/config.yml", "c4:\n  mode: enforce\n")
	target := commitAll(t, dir, "policy")

	git(t, dir, "checkout", "-q", "-b", "move")
	dsl := git(t, dir, "show", "HEAD:.nugit/architecture/workspace.dsl")
	git(t, dir, "rm", "-q", ".nugit/architecture/workspace.dsl")
	write(t, dir, "model/workspace.dsl", dsl+"\n")
	write(t, dir, ".nugit/config.yml", "c4:\n  mode: enforce\n  dsl: model/workspace.dsl\n")
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\nfunc A() {}\n")
	head := commitAll(t, dir, "move the model")

	rep, err := BuildReport(Options{RepoDir: dir, Base: target, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if f := hasC4Fail(rep); f != nil {
		t.Fatalf("the edge is declared in the moved model, read at head: %+v", f)
	}
	if fs := findings(rep, "enforcement-config"); len(fs) != 0 {
		t.Errorf("c4.dsl is a head key, never a governed change: %+v", fs)
	}
}

// An explicit -fail-on weaker than the GOVERNING policy is a visible
// downgrade (ADR-0026), and so is one that only the PR's own config allows.
func TestDowngradeIsMeasuredAgainstTheTarget(t *testing.T) {
	dir, _ := seed(t, true)
	write(t, dir, ".nugit/config.yml", "pr_render:\n  fail_on: fail\n")
	target := commitAll(t, dir, "policy")
	git(t, dir, "checkout", "-q", "-b", "x")
	write(t, dir, ".nugit/config.yml", "pr_render:\n  fail_on: none\n")
	head := commitAll(t, dir, "relax")

	rep, err := BuildReport(Options{RepoDir: dir, Base: target, Head: head, FailOnFlag: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Enforcement == nil {
		t.Fatal("-fail-on none against the target's fail is a downgrade, whatever the PR's config says")
	}
}
