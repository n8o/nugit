package engine

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// THE HARD REQUIREMENT (ADR-0032): an absent peer must NEVER fail pr-render.
// CI has only the repo under review checked out, so "the sibling isn't here" is
// the NORMAL state, not an error — a federation feature that reddened every CI
// run the moment a sibling moved would be uninstallable.
//
// It is also a structural guarantee, not just a graceful one: the pr-render
// pipeline reads the store at the reviewed REF and never consults peers at all,
// so peer knowledge cannot reach a finding, a significance class, or the
// c4<->code gate. This test pins that.
func TestAbsentPeerNeverFailsPRRender(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/config.yml",
		"schema_version: 1\nc4:\n  mode: enforce\npr_render:\n  fail_on: fail\npeers:\n  - name: platform\n    path: ../not-checked-out\n")
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feat: a uses b")
	head := git(t, dir, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatalf("an absent peer must never error pr-render: %v", err)
	}
	for _, f := range rep.Findings {
		if f.Severity == model.SevFail {
			t.Errorf("an absent peer produced a FAIL finding (exit non-zero): %s — %s", f.Check, f.Title)
		}
	}
}

// The same with a peer that IS present: peer knowledge is retrieval-and-context
// only in phase 1. It must not reach enforcement — no finding, no significance
// shift, no c4<->code effect.
func TestPresentPeerDoesNotAffectEnforcement(t *testing.T) {
	dir, base := seed(t, true)
	peer := t.TempDir()
	write(t, peer, ".nugit/decisions/g.md",
		"---\nschema_version: 1\nid: ADR-0001\ntype: decision\nscope: global\nstatus: accepted\n"+
			"created: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# ADR-0001\n\npeer decision\n")
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feat: a uses b")
	head := git(t, dir, "rev-parse", "HEAD")

	baseline, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".nugit/config.yml",
		"schema_version: 1\nc4:\n  mode: enforce\npr_render:\n  fail_on: fail\npeers:\n  - name: platform\n    path: "+peer+"\n")
	withPeer, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Findings) != len(withPeer.Findings) {
		t.Errorf("a configured peer changed the finding set: %d -> %d", len(baseline.Findings), len(withPeer.Findings))
	}
	if baseline.Significance.Tier != withPeer.Significance.Tier {
		t.Errorf("a configured peer changed significance: %s -> %s",
			baseline.Significance.Tier, withPeer.Significance.Tier)
	}
}
