package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/render"
)

// These tests drive the ORG-LANDSCAPE ownership check (ADR-0034) end to end
// through BuildReport, because the wiring — config → engine → landscape
// resolution → check — is the part that can silently stop working, not the
// predicate. The scenario throughout is the motivating one: repo A holds the
// files that configure a system repo B owns.

// landscapeDSL declares a repo-backed system plus one shared system owned by
// `owner`, configured through paths that live in the READING repo's tree.
func landscapeDSL(owner string) string {
	return `workspace {
  model {
    gateway = softwareSystem "Consumer Gateway" {
      properties { "nugit_repo" "consumer-gateway" }
    }
    buildcluster = softwareSystem "Shared build cluster" {
      properties {
        "nugit_owner" "` + owner + `"
        "nugit_paths" "ci/cluster/**,deploy/runners.yaml"
      }
    }
    gateway -> buildcluster "runs CI on"
  }
}
`
}

// landscapeCfg writes a config with an optional identity, peers, and nothing
// else that could alter the render.
func landscapeCfg(orgRepo string, peers map[string]string) string {
	s := "schema_version: 1\nc4:\n  mode: enforce\npr_render:\n  fail_on: fail\n"
	if orgRepo != "" {
		s += "org:\n  repo: " + orgRepo + "\n"
	}
	if len(peers) > 0 {
		s += "peers:\n"
		for _, name := range sortedKeys(peers) {
			s += "  - name: " + name + "\n    path: " + peers[name] + "\n"
		}
	}
	return s
}

func sortedKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	for i := range out { // tiny insertion sort: deterministic without importing sort
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func landscapeFindings(rep model.Report) []model.Finding {
	var out []model.Finding
	for _, f := range rep.Findings {
		if f.Check == "landscape-ownership" {
			out = append(out, f)
		}
	}
	return out
}

// THE HEADLINE CASE: a PR in repo A changes the files that configure a cluster
// repo B owns. A's own render says so, names the owner, and tells the author
// what to do about it.
func TestOwnershipWarnsWhenConfiguringAnotherReposSystem(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/architecture/landscape.dsl", landscapeDSL("producer-service"))
	write(t, dir, ".nugit/config.yml", landscapeCfg("consumer-gateway", nil))
	commitAll(t, dir, "chore: adopt the landscape")
	base = git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "ci/cluster/priority.yaml", "schedulingPriority: 100\n")
	write(t, dir, "deploy/runners.yaml", "replicas: 8\n")
	write(t, dir, "a/a.go", "package a\n\nfunc A() {}\nfunc A2() {}\n")
	head := commitAll(t, dir, "chore: bump runner priority")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	fs := landscapeFindings(rep)
	if len(fs) != 1 {
		t.Fatalf("want exactly one ownership warning, got %d: %+v", len(fs), fs)
	}
	f := fs[0]
	if f.Severity != model.SevWarn {
		t.Errorf("severity = %s, want warn (ADR-0034 point 4)", f.Severity)
	}
	line := f.Title + " " + f.Detail
	for _, want := range []string{
		"Shared build cluster",     // the system, by name
		"buildcluster",             // and by id, so it is findable in the DSL
		"producer-service",         // the owner
		"consumer-gateway",         // who this repo is
		"ci/cluster/priority.yaml", // the matched paths
		"deploy/runners.yaml",
		"coordinate with producer-service", // what to DO
	} {
		if !strings.Contains(line, want) {
			t.Errorf("finding must name %q; got:\n%s\n%s", want, f.Title, f.Detail)
		}
	}
	// The unrelated Go file must not be listed as configuring the cluster.
	if strings.Contains(f.Detail, "a/a.go") {
		t.Errorf("unmatched file leaked into the finding: %s", f.Detail)
	}
	// And no fail-severity finding may come out of a peer-facing feature.
	for _, x := range rep.Findings {
		if x.Check == "landscape-ownership" && x.Severity == model.SevFail {
			t.Error("landscape-ownership must never reach fail severity")
		}
	}
}

// A system this repo OWNS is this repo's business: silence.
func TestOwnershipSilentForASystemThisRepoOwns(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/architecture/landscape.dsl", landscapeDSL("consumer-gateway"))
	write(t, dir, ".nugit/config.yml", landscapeCfg("consumer-gateway", nil))
	commitAll(t, dir, "chore: adopt the landscape")
	base = git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "ci/cluster/priority.yaml", "schedulingPriority: 100\n")
	head := commitAll(t, dir, "chore: our own cluster")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := landscapeFindings(rep); len(fs) != 0 {
		t.Fatalf("a repo editing the system it owns must be silent, got %+v", fs)
	}
}

// With no `org.repo` the check is INERT — nugit never guesses which repo this
// is, so it cannot know whether it is the owner (ADR-0033 point 3's rule,
// inherited verbatim).
func TestOwnershipInertWithoutOrgRepo(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/architecture/landscape.dsl", landscapeDSL("producer-service"))
	write(t, dir, ".nugit/config.yml", landscapeCfg("", nil))
	commitAll(t, dir, "chore: adopt the landscape")
	base = git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "ci/cluster/priority.yaml", "schedulingPriority: 100\n")
	head := commitAll(t, dir, "chore: bump")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := landscapeFindings(rep); len(fs) != 0 {
		t.Fatalf("no identity ⇒ inert, got %+v", fs)
	}
}

// Everything the verdict depends on is read at the REVIEWED REF, never the
// working tree (LESSON-read-from-reviewed-ref, ADR-0029): an uncommitted edit
// to a configuring file must not produce a finding, and an uncommitted
// landscape.dsl must not be consulted at all.
func TestOwnershipReadsChangedFilesAndLandscapeAtTheReviewedRef(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/config.yml", landscapeCfg("consumer-gateway", nil))
	commitAll(t, dir, "chore: identity only")
	base = git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "a/a.go", "package a\n\nfunc A() {}\nfunc A3() {}\n")
	head := commitAll(t, dir, "chore: unrelated")

	// Dirty the checkout AFTER head: a landscape and a configuring file, neither
	// of which exists at the reviewed ref.
	write(t, dir, ".nugit/architecture/landscape.dsl", landscapeDSL("producer-service"))
	write(t, dir, "ci/cluster/priority.yaml", "schedulingPriority: 100\n")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := landscapeFindings(rep); len(fs) != 0 {
		t.Fatalf("working-tree state must not reach the verdict, got %+v", fs)
	}

	// Commit both and the same range-at-a-new-head now fires.
	head2 := commitAll(t, dir, "chore: land the landscape and the config")
	rep2, err := BuildReport(Options{RepoDir: dir, Base: head, Head: head2})
	if err != nil {
		t.Fatal(err)
	}
	if fs := landscapeFindings(rep2); len(fs) != 1 {
		t.Fatalf("committed state must fire, got %+v", fs)
	}
}

// The landscape may live in a SIBLING repo: this repo declares none, exactly
// one peer does, and that peer's landscape governs this repo's view (ADR-0032
// transport, ADR-0011 single writer). The finding says where it came from.
func TestOwnershipUsesAPeersLandscapeWhenThisRepoHasNone(t *testing.T) {
	dir, base := seed(t, true)
	peer := t.TempDir()
	write(t, peer, ".nugit/architecture/landscape.dsl", landscapeDSL("producer-service"))
	write(t, dir, ".nugit/config.yml", landscapeCfg("consumer-gateway", map[string]string{"producer": peer}))
	commitAll(t, dir, "chore: peer the producer")
	base = git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "ci/cluster/priority.yaml", "schedulingPriority: 100\n")
	head := commitAll(t, dir, "chore: bump")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	fs := landscapeFindings(rep)
	if len(fs) != 1 {
		t.Fatalf("want the peer-sourced warning, got %+v", fs)
	}
	if !strings.Contains(fs[0].Detail, "peer producer:") {
		t.Errorf("the finding must say the landscape is foreign: %s", fs[0].Detail)
	}
	// An absent peer degrades to nothing and can never error (ADR-0032 point 3).
	if err := os.RemoveAll(filepath.Join(peer, ".nugit")); err != nil {
		t.Fatal(err)
	}
	rep2, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatalf("an absent peer must never fail pr-render: %v", err)
	}
	if fs := landscapeFindings(rep2); len(fs) != 0 {
		t.Errorf("no landscape reachable ⇒ no finding, got %+v", fs)
	}
}

// Two peers each declaring a landscape is an ADR-0011 violation IN THE ORG.
// nugit refuses to break the tie — picking by configured order would make the
// org's shared model depend on this reader's private, reorderable peer list.
func TestOwnershipUsesNothingWhenTwoPeersDeclareALandscape(t *testing.T) {
	dir, base := seed(t, true)
	p1, p2 := t.TempDir(), t.TempDir()
	write(t, p1, ".nugit/architecture/landscape.dsl", landscapeDSL("producer-service"))
	write(t, p2, ".nugit/architecture/landscape.dsl", landscapeDSL("other-service"))
	write(t, dir, ".nugit/config.yml", landscapeCfg("consumer-gateway",
		map[string]string{"alpha": p1, "beta": p2}))
	commitAll(t, dir, "chore: two peers")
	base = git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "ci/cluster/priority.yaml", "schedulingPriority: 100\n")
	head := commitAll(t, dir, "chore: bump")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := landscapeFindings(rep); len(fs) != 0 {
		t.Fatalf("ambiguity must fail closed to NO landscape, got %+v", fs)
	}
	// A local landscape ends the ambiguity outright: it always wins, and no peer
	// landscape is even read.
	write(t, dir, ".nugit/architecture/landscape.dsl", landscapeDSL("producer-service"))
	write(t, dir, "ci/cluster/priority.yaml", "schedulingPriority: 200\n")
	head2 := commitAll(t, dir, "chore: declare our own")
	rep2, err := BuildReport(Options{RepoDir: dir, Base: head, Head: head2})
	if err != nil {
		t.Fatal(err)
	}
	local := landscapeFindings(rep2)
	if len(local) != 1 {
		t.Fatalf("a local landscape must win outright, got %+v", local)
	}
	if strings.Contains(local[0].Detail, "peer ") {
		t.Errorf("a local landscape must not be attributed to a peer: %s", local[0].Detail)
	}
}

// THE REGRESSION GUARANTEE (ADR-0034 point 2). A repo with no landscape must
// render byte-for-byte what it rendered before this decision existed, and a
// landscape that is present but inert (no identity) must not perturb one byte
// of it either. If this test ever fails, the layering has leaked.
func TestNoLandscapeMeansByteIdenticalPRRender(t *testing.T) {
	baseline := func(t *testing.T, extra func(dir string)) string {
		t.Helper()
		dir, base := seed(t, true)
		write(t, dir, "README.md", "demo\n")
		if extra != nil {
			extra(dir)
		}
		commitAll(t, dir, "chore: setup")
		base = git(t, dir, "rev-parse", "HEAD")
		// A change that touches paths a landscape WOULD have claimed.
		write(t, dir, "ci/cluster/priority.yaml", "schedulingPriority: 100\n")
		write(t, dir, "a/a.go", "package a\n\nfunc A() {}\nfunc A4() {}\n")
		head := commitAll(t, dir, "chore: bump runner priority")
		rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
		if err != nil {
			t.Fatal(err)
		}
		out := render.Markdown(rep)
		// Refs differ per temp repo; the rest must match byte for byte.
		out = strings.ReplaceAll(out, rep.BaseRef, "BASE")
		return strings.ReplaceAll(out, rep.HeadRef, "HEAD")
	}

	plain := baseline(t, nil)
	withInertLandscape := baseline(t, func(dir string) {
		// The landscape exists and claims these very paths, but no identity is
		// configured, so the whole feature is inert.
		write(t, dir, ".nugit/architecture/landscape.dsl", landscapeDSL("producer-service"))
	})
	if plain != withInertLandscape {
		t.Errorf("an inert landscape perturbed pr-render output\n--- plain ---\n%s\n--- with landscape ---\n%s",
			plain, withInertLandscape)
	}
}
