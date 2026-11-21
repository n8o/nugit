package engine

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func commitMsg(t *testing.T, dir, msg string) string {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

// ADR-0056: the PR view lists the range's unverified commits. Under observe
// that is all it does; under enforce an unexplained one warns.
func TestVerificationView(t *testing.T) {
	for _, gate := range []string{"observe", "enforce"} {
		t.Run(gate, func(t *testing.T) {
			dir, _ := seed(t, true)
			write(t, dir, ".nugit/config.yml", "verify:\n  gate: "+gate+"\n")
			target := commitAll(t, dir, "policy")
			git(t, dir, "checkout", "-q", "-b", "work")
			write(t, dir, "a/a.go", "package a\n\n// A does a thing, now better.\nfunc A() {}\n")
			commitMsg(t, dir, "feat: a\n\nverified: e2e, tests")
			commitMsg(t, dir, "docs: b\n\nverified: none — docs-only change")
			head := commitMsg(t, dir, "fix: c\n\nverified: none")

			rep, err := BuildReport(Options{RepoDir: dir, Base: target, Head: head})
			if err != nil {
				t.Fatal(err)
			}
			v := rep.Verification
			if v == nil || v.Commits != 3 || v.Verified != 1 || v.ByKind["e2e"] != 1 || len(v.Unverified) != 2 {
				t.Fatalf("view: %+v", v)
			}
			if v.Unverified[0].Note != "docs-only change" || v.Unverified[1].Note != "" || !v.Unverified[1].Stamped {
				t.Errorf("unverified: %+v", v.Unverified)
			}
			fs := findings(rep, "unverified-commits")
			if gate == "observe" && len(fs) != 0 {
				t.Errorf("observe never warns: %+v", fs)
			}
			if gate == "enforce" {
				if len(fs) != 1 || fs[0].Severity != model.SevWarn || !strings.Contains(fs[0].Detail, "fix: c") ||
					strings.Contains(fs[0].Detail, "docs: b") {
					t.Errorf("enforce warns on the unexplained commit only: %+v", fs)
				}
			}
		})
	}
}

// A repo that never stamped a commit sees nothing new under the default
// observe, and a PR cannot hide its commits by switching the gate off in its
// own config: the gate mode is governed (ADR-0055).
func TestVerificationViewQuietUntilUsedAndGoverned(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, "a/a.go", "package a\n\n// A does a thing, now better.\nfunc A() {}\n")
	head := commitAll(t, dir, "feat: unstamped")
	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verification != nil {
		t.Errorf("observe with no stamps renders nothing: %+v", rep.Verification)
	}

	dir, _ = seed(t, true)
	write(t, dir, ".nugit/config.yml", "verify:\n  gate: enforce\n")
	target := commitAll(t, dir, "policy")
	git(t, dir, "checkout", "-q", "-b", "work")
	write(t, dir, ".nugit/config.yml", "verify:\n  gate: off\n")
	head = commitAll(t, dir, "feat: turn the gate off")
	rep, err = BuildReport(Options{RepoDir: dir, Base: target, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verification == nil || len(findings(rep, "unverified-commits")) != 1 {
		t.Fatalf("the target's enforce governs: %+v %+v", rep.Verification, rep.Findings)
	}
	if len(findings(rep, "enforcement-config")) != 1 {
		t.Errorf("turning the gate off is a governed change: %+v", rep.Findings)
	}
}
