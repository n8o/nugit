package retrieval

import (
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end over real temp checkouts: with a hub designated, the HUB's
// landscape is the org's answer even when another peer also ships one — the
// ADR-0034 ambiguity that used to fail closed to nothing (ADR-0035 point 2).

func landscapeDSL(systemID, owner, paths string) string {
	return "workspace {\n  model {\n    " + systemID + " = softwareSystem \"" + systemID + "\" {\n" +
		"      properties {\n        \"nugit_owner\" \"" + owner + "\"\n" +
		"        \"nugit_paths\" \"" + paths + "\"\n      }\n    }\n  }\n}\n"
}

// buildOrg lays out member/ + hub/ + other/, with member configuring both peers
// and designating the hub. Both peers ship a landscape; only the hub's may win.
func buildOrg(t *testing.T, withHub bool) string {
	t.Helper()
	root := t.TempDir()
	member := filepath.Join(root, "member")

	cfg := "schema_version: 1\nc4:\n  mode: warn\norg:\n  repo: member-repo\n"
	if withHub {
		cfg += "  hub: hub\n"
	}
	cfg += "peers:\n  - name: hub\n    path: ../hub\n  - name: other\n    path: ../other\n"
	put(t, member, ".nugit/config.yml", cfg)
	put(t, member, ".nugit/architecture/workspace.dsl",
		"workspace { model { s = softwareSystem \"s\" { app = container \"app\" {\n"+
			"  cfgc = component \"cfg\" { properties { \"paths\" \"deploy/**\" } }\n} } } }")

	put(t, filepath.Join(root, "hub"), ".nugit/config.yml", "schema_version: 1\norg:\n  repo: org-hub\n")
	put(t, filepath.Join(root, "hub"), ".nugit/architecture/landscape.dsl",
		landscapeDSL("registry", "hub-owner", "deploy/**"))

	put(t, filepath.Join(root, "other"), ".nugit/config.yml", "schema_version: 1\norg:\n  repo: other-repo\n")
	put(t, filepath.Join(root, "other"), ".nugit/architecture/landscape.dsl",
		landscapeDSL("registry", "other-owner", "deploy/**"))

	put(t, member, "deploy/registry.yaml", "x: 1\n")
	return member
}

func TestHubLandscapeWinsOverAnotherPeersEndToEnd(t *testing.T) {
	member := buildOrg(t, true)
	b, err := Context(Options{RepoDir: member, Path: "deploy/registry.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Landscape) == 0 {
		t.Fatal("no landscape resolved — the hub's must win outright over the other peer's (ADR-0035)")
	}
	got := b.Landscape[0]
	if got.Owner != "hub-owner" {
		t.Errorf("owner = %q, want the HUB's declaration, not the other peer's", got.Owner)
	}
	if got.Origin != "hub" {
		t.Errorf("origin = %q, want hub", got.Origin)
	}
}

// The pre-hub behaviour must be exactly preserved when no hub is designated:
// two peers each declaring a landscape is ambiguous and NOTHING is used
// (ADR-0034 point 3.3).
func TestWithoutAHubTwoPeerLandscapesStillResolveToNothing(t *testing.T) {
	member := buildOrg(t, false)
	b, err := Context(Options{RepoDir: member, Path: "deploy/registry.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Landscape) != 0 {
		t.Errorf("ambiguity resolved to %+v — without a hub nothing may be used", b.Landscape)
	}
}

// A designated hub that is not checked out degrades to the ordinary rules and
// never errors: this is the normal CI state (ADR-0032 point 3).
func TestUnreachableHubDegradesWithoutFailing(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "member")
	put(t, member, ".nugit/config.yml",
		"schema_version: 1\nc4:\n  mode: warn\norg:\n  repo: member-repo\n  hub: hub\n"+
			"peers:\n  - name: hub\n    path: ../hub\n  - name: other\n    path: ../other\n")
	put(t, member, ".nugit/architecture/workspace.dsl",
		"workspace { model { s = softwareSystem \"s\" { app = container \"app\" {\n"+
			"  cfgc = component \"cfg\" { properties { \"paths\" \"deploy/**\" } }\n} } } }")
	// The hub directory does not exist at all; `other` does.
	put(t, filepath.Join(root, "other"), ".nugit/config.yml", "schema_version: 1\norg:\n  repo: other-repo\n")
	put(t, filepath.Join(root, "other"), ".nugit/architecture/landscape.dsl",
		landscapeDSL("registry", "other-owner", "deploy/**"))
	put(t, member, "deploy/registry.yaml", "x: 1\n")

	b, err := Context(Options{RepoDir: member, Path: "deploy/registry.yaml"})
	if err != nil {
		t.Fatalf("an absent hub failed retrieval: %v", err)
	}
	// A hub with no landscape is not a claim, so the single remaining peer wins.
	if len(b.Landscape) != 1 || b.Landscape[0].Origin != "other" {
		t.Errorf("landscape = %+v, want the single reachable peer's", b.Landscape)
	}
	if strings.Contains(b.Path, "hub") {
		t.Errorf("unexpected hub reference in the bundle path %q", b.Path)
	}
}
