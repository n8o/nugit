package engine

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// A sibling took decision number 0051 after this branch was cut, under its own
// id. This branch adds 0051 under another. Neither the ids nor this branch's
// history collide; the target's tip does.
func TestNumberTakenOnTargetFailsTheSecondPR(t *testing.T) {
	dir, base := seed(t, true)

	write(t, dir, ".nugit/decisions/0051-fabrics-provider.md", adr("ADR-0051", "fabrics provider"))
	target := commitAll(t, dir, "sibling: ADR-0051")

	git(t, dir, "checkout", "-q", "-b", "mine", base)
	write(t, dir, ".nugit/decisions/0051-offline-access.md", adr("ADR-0052", "offline access"))
	mine := commitAll(t, dir, "mine: 0051 file, ADR-0052 id")

	rep, err := BuildReport(Options{RepoDir: dir, Base: target, Head: mine})
	if err != nil {
		t.Fatal(err)
	}
	if fs := findings(rep, "duplicate-knowledge-id"); len(fs) != 0 {
		t.Fatalf("the ids differ, so the id check must stay silent: %+v", fs)
	}
	fs := findings(rep, "duplicate-decision-number")
	if len(fs) != 1 {
		t.Fatalf("want one duplicate-decision-number finding, got %d: %+v", len(fs), fs)
	}
	if fs[0].Severity != model.SevFail || !strings.Contains(fs[0].Detail, "0051-fabrics-provider.md") {
		t.Errorf("want a fail naming the sibling's file, got %+v", fs[0])
	}

	// From the merge base there is nothing to collide with.
	rep2, err := BuildReport(Options{RepoDir: dir, Base: base, Head: mine})
	if err != nil {
		t.Fatal(err)
	}
	if fs2 := findings(rep2, "duplicate-decision-number"); len(fs2) != 0 {
		t.Errorf("against the merge base there is no collision to see, got %+v", fs2)
	}
}

// Once the branch has merged the target, both records are in its tree, and
// the one this PR added is what the within-PR check reports.
func TestNumberSharedInTheTreeFailsThePRThatAddsIt(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/decisions/0051-a.md", adr("ADR-0051", "a"))
	commitAll(t, dir, "ADR-0051 lands")

	git(t, dir, "checkout", "-q", "-b", "half-renumbered")
	write(t, dir, ".nugit/decisions/0051-b.md", adr("ADR-0052", "b"))
	head := commitAll(t, dir, "0051 file, ADR-0052 id")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := findings(rep, "duplicate-decision-number"); len(fs) != 1 {
		t.Fatalf("one shared number, one finding — got %d: %+v", len(fs), fs)
	}
}
