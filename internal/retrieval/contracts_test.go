package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contractDoc(id, party, status string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: contract\nscope: global\nstatus: " + status +
		"\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n" +
		"parties:\n  - repo: " + party + "\n    must:\n" +
		"      - name: mirror guard present\n        file: apps/gateway/server.cpp\n        matches: 'useStandardProtocols'\n" +
		"---\n\n# " + id + " — the transport contract\n\nboth halves or neither.\n"
}

func ctxRepo(t *testing.T, orgRepo, peerDir string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := "schema_version: 1\n"
	if orgRepo != "" {
		cfg += "org:\n  repo: " + orgRepo + "\n"
	}
	if peerDir != "" {
		cfg += "peers:\n  - name: producer\n    path: " + peerDir + "\n"
	}
	put(t, dir, ".nugit/config.yml", cfg)
	put(t, dir, ".nugit/architecture/workspace.dsl",
		"workspace \"d\" {\n model {\n  s = softwareSystem \"d\" {\n"+
			"   gw = component \"GW\" { properties { paths \"apps/gateway/**\" } }\n"+
			"  }\n }\n}\n")
	return dir
}

// A peer's contract naming this repo is high-value context: it is what this
// repo OWES, labelled with where it came from.
func TestPeerContractSurfacesInTheBundleLabeledWithItsOrigin(t *testing.T) {
	peer := t.TempDir()
	put(t, peer, ".nugit/contracts/transport.md", contractDoc("CONTRACT-0001", "consumer-gateway", "accepted"))
	dir := ctxRepo(t, "consumer-gateway", peer)

	b, err := Context(Options{RepoDir: dir, Path: "apps/gateway/server.cpp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Contracts) != 1 {
		t.Fatalf("want the peer contract in the bundle, got %+v", b.Contracts)
	}
	c := b.Contracts[0]
	if c.QualifiedID() != "producer:CONTRACT-0001" || c.Origin != "producer" {
		t.Errorf("a foreign contract must render qualified and carry its origin: %+v", c)
	}
	md := b.Markdown()
	if !strings.Contains(md, "**Contracts**") || !strings.Contains(md, "producer:CONTRACT-0001") ||
		!strings.Contains(md, "peer producer") {
		t.Errorf("markdown must show the contract and say where it came from:\n%s", md)
	}
}

// A contract naming a DIFFERENT repo is not this repo's context, and with no
// identity configured nothing is surfaced at all — the same inert-by-default
// rule the check follows.
func TestContractsForOtherPartiesAndWithoutIdentityStayOut(t *testing.T) {
	peer := t.TempDir()
	put(t, peer, ".nugit/contracts/transport.md", contractDoc("CONTRACT-0001", "some-other-repo", "accepted"))

	if b, err := Context(Options{RepoDir: ctxRepo(t, "consumer-gateway", peer), Path: "apps/gateway/server.cpp"}); err != nil {
		t.Fatal(err)
	} else if len(b.Contracts) != 0 {
		t.Errorf("a contract naming another repo must not surface here: %+v", b.Contracts)
	}

	peer2 := t.TempDir()
	put(t, peer2, ".nugit/contracts/transport.md", contractDoc("CONTRACT-0001", "consumer-gateway", "accepted"))
	if b, err := Context(Options{RepoDir: ctxRepo(t, "", peer2), Path: "apps/gateway/server.cpp"}); err != nil {
		t.Fatal(err)
	} else if len(b.Contracts) != 0 {
		t.Errorf("with no org.repo nothing may surface — nugit never guesses which party it is: %+v", b.Contracts)
	}
}

// Unratified contracts are candidates, not obligations, in retrieval too.
func TestUnratifiedContractDoesNotSurface(t *testing.T) {
	peer := t.TempDir()
	put(t, peer, ".nugit/contracts/transport.md", contractDoc("CONTRACT-0001", "consumer-gateway", "proposed"))
	b, err := Context(Options{RepoDir: ctxRepo(t, "consumer-gateway", peer), Path: "apps/gateway/server.cpp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Contracts) != 0 {
		t.Errorf("a proposed contract is a candidate, not an obligation: %+v", b.Contracts)
	}
}

// A local and a peer contract minting the SAME id stay two records in the
// bundle (ADR-0032 identity), and the local one sorts first.
func TestSameIDContractsBothSurfaceAndLocalRanksFirst(t *testing.T) {
	peer := t.TempDir()
	put(t, peer, ".nugit/contracts/transport.md", contractDoc("CONTRACT-0001", "consumer-gateway", "accepted"))
	dir := ctxRepo(t, "consumer-gateway", peer)
	put(t, dir, ".nugit/contracts/local.md", contractDoc("CONTRACT-0001", "consumer-gateway", "accepted"))

	b, err := Context(Options{RepoDir: dir, Path: "apps/gateway/server.cpp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Contracts) != 2 {
		t.Fatalf("two stores' CONTRACT-0001s must stay distinct, got %+v", b.Contracts)
	}
	if b.Contracts[0].Origin != "" || b.Contracts[1].Origin != "producer" {
		t.Errorf("local must rank before peer: %+v", b.Contracts)
	}
}

// The contract section fills ahead of decisions but must NEVER displace the
// single spec slot — the spec is the path's active contract with its own code.
func TestContractsNeverDisplaceTheSpecSlot(t *testing.T) {
	peer := t.TempDir()
	put(t, peer, ".nugit/contracts/transport.md", contractDoc("CONTRACT-0001", "consumer-gateway", "accepted"))
	dir := ctxRepo(t, "consumer-gateway", peer)
	put(t, dir, ".nugit/specs/s.md", "---\nschema_version: 1\nid: SPEC-001\ntype: spec\nscope: gw\n"+
		"status: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# SPEC-001 — gateway\n\nbody\n")

	// A budget tight enough that something must go.
	b, err := Context(Options{RepoDir: dir, Path: "apps/gateway/server.cpp", BudgetTokens: 40})
	if err != nil {
		t.Fatal(err)
	}
	if b.Spec == nil {
		t.Fatal("the spec slot was displaced by a contract")
	}
	// And whatever was cut is REPORTED, never silently dropped.
	if b.Truncated && len(b.Dropped) == 0 {
		t.Error("a truncated bundle must always list what it dropped")
	}
}

// Budget pressure drops a contract loudly, like every other kind.
func TestDroppedContractIsReported(t *testing.T) {
	peer := t.TempDir()
	put(t, peer, ".nugit/contracts/transport.md", contractDoc("CONTRACT-0001", "consumer-gateway", "accepted"))
	dir := ctxRepo(t, "consumer-gateway", peer)

	b, err := Context(Options{RepoDir: dir, Path: "apps/gateway/server.cpp", BudgetTokens: 25})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Contracts) != 0 {
		t.Skip("budget was not tight enough to force a drop")
	}
	if !b.Truncated || !strings.Contains(strings.Join(b.Dropped, "; "), "contract producer:CONTRACT-0001") {
		t.Errorf("a dropped contract must appear in Dropped[]: truncated=%v dropped=%v", b.Truncated, b.Dropped)
	}
}
