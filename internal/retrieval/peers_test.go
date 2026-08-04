package retrieval

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// ---- fixtures: two real stores on disk, in two temp repos ----

// statusObj is obj() with an explicit status (obj() hardcodes accepted).
func statusObj(id, typ, scope, status, body, relates string) string {
	s := "---\nschema_version: 1\nid: " + id + "\ntype: " + typ + "\nscope: " + scope +
		"\nstatus: " + status + "\ncreated: 2026-01-01T00:00:00Z\n"
	if relates != "" {
		s += "relates_to:\n  - " + relates + "\n"
	}
	return s + "provenance:\n  commit: x\n---\n\n# " + id + " title\n\n" + body + "\n"
}

// supersedingObj mints an object whose front-matter supersedes `target`.
func supersedingObj(id, typ, scope, target, body string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: " + typ + "\nscope: " + scope +
		"\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nsupersedes: " + target +
		"\nprovenance:\n  commit: x\n---\n\n# " + id + " title\n\n" + body + "\n"
}

// federated builds a local repo plus a sibling checkout and points the local
// config at it under the name "platform". Returns the local repo dir.
func federated(t *testing.T, peerFiles map[string]string) (local, peer string) {
	t.Helper()
	local = setup(t)
	peer = t.TempDir()
	wf(t, local, ".nugit/config.yml", "schema_version: 1\npeers:\n  - name: platform\n    path: "+peer+"\n")
	for rel, content := range peerFiles {
		wf(t, peer, rel, content)
	}
	return local, peer
}

func originOf(items []Item, id string) (string, bool) {
	for _, it := range items {
		if it.ID == id {
			return it.Origin, true
		}
	}
	return "", false
}

func qualified(items []Item) map[string]bool {
	m := map[string]bool{}
	for _, it := range items {
		m[it.QualifiedID()] = true
	}
	return m
}

// ---- reach: what a peer contributes ----

// A peer's GLOBAL, ratified knowledge reaches a local bundle, labeled with its
// origin — the whole point of ADR-0032.
func TestPeerGlobalSurfacesWithOrigin(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/mirror.md": statusObj("ADR-0020", "decision", "global", "accepted",
			"the registry retention window must be configured on both sides", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "registry retention window"})
	if err != nil {
		t.Fatal(err)
	}
	org, ok := originOf(b.Decisions, "ADR-0020")
	if !ok {
		t.Fatalf("peer global decision missing from the bundle: %+v", b.Decisions)
	}
	if org != "platform" {
		t.Errorf("origin = %q, want platform", org)
	}
	if !qualified(b.Decisions)["platform:ADR-0020"] {
		t.Errorf("foreign id must display qualified: %v", qualified(b.Decisions))
	}
	md := b.Markdown()
	if !strings.Contains(md, "`platform:ADR-0020`") || !strings.Contains(md, "peer platform") {
		t.Errorf("markdown must name the peer and qualify the id:\n%s", md)
	}
	// The JSON contract carries the origin too — an agent must not have to
	// parse the display string to know this is foreign.
	raw, _ := json.Marshal(b)
	if !strings.Contains(string(raw), `"origin":"platform"`) {
		t.Errorf("json render must carry the origin field: %s", raw)
	}
}

// A peer's COMPONENT-scoped knowledge names a component id that means nothing
// here: `render` in the sibling and `render` in this model are unrelated
// strings that happen to match. It must never surface.
func TestPeerComponentScopedDoesNotSurface(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/r.md": statusObj("ADR-PEERCOMP", "decision", "render", "accepted",
			"peer render decision about rendering", ""),
		".nugit/lessons/r.md": statusObj("LESSON-PEERCOMP", "lesson", "render", "accepted",
			"peer render lesson about rendering", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "rendering"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := originOf(b.Decisions, "ADR-PEERCOMP"); ok {
		t.Error("a peer's component-scoped decision must NOT surface — its scope names nothing here")
	}
	if _, ok := originOf(b.Lessons, "LESSON-PEERCOMP"); ok {
		t.Error("a peer's component-scoped lesson must NOT surface")
	}
}

// Someone else's unreviewed candidate is not context: the candidate lane is a
// LOCAL review queue (ADR-0016) and nobody here can ratify a foreign draft.
func TestUnratifiedPeerObjectDoesNotSurface(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/p.md": statusObj("ADR-PROPOSED", "decision", "global", "proposed",
			"a proposed peer decision about rendering", ""),
		".nugit/decisions/live.md": statusObj("ADR-LIVE", "decision", "global", "accepted",
			"a ratified peer decision about rendering", ""),
		".nugit/decisions/dead.md": statusObj("ADR-DEAD", "decision", "global", "invalidated",
			"an invalidated peer decision about rendering", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "rendering"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := originOf(b.Decisions, "ADR-PROPOSED"); ok {
		t.Error("an unratified (proposed) peer object must NOT surface")
	}
	if _, ok := originOf(b.Decisions, "ADR-DEAD"); ok {
		t.Error("an invalidated peer object must NOT surface")
	}
	if _, ok := originOf(b.Decisions, "ADR-LIVE"); !ok {
		t.Error("a ratified peer global must surface")
	}
}

// A peer's spec never takes the single spec slot, and a peer glossary never
// defines terms here (ADR-0032 restricts foreign kinds to decision/lesson/reference).
func TestPeerSpecAndGlossaryStayLocal(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/specs/s.md": statusObj("SPEC-PEER", "spec", "global", "accepted",
			"a peer spec about rendering", ""),
		".nugit/glossary.md": statusObj("GLOSSARY", "glossary", "global", "accepted",
			"- **rendering** — the peer's own definition", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "rendering"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Spec != nil && b.Spec.Origin != "" {
		t.Errorf("the spec slot must stay local, got %s from peer %s", b.Spec.ID, b.Spec.Origin)
	}
	for _, g := range b.Glossary {
		if strings.Contains(g, "the peer's own definition") {
			t.Errorf("a peer glossary term leaked into the bundle: %q", g)
		}
	}
}

// ---- the namespace collision: the central correctness problem ----

// Every repo mints ADR-0001. Two stores carrying the SAME id must both survive
// the merge as distinct objects, each with its own summary and origin.
func TestIdenticalIDsInBothStoresStayDistinct(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/g.md": statusObj("ADR-G", "decision", "global", "accepted",
			"the PEER decision that happens to share an id", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	var localHit, peerHit *Item
	for i := range b.Decisions {
		if b.Decisions[i].ID != "ADR-G" {
			continue
		}
		if b.Decisions[i].Origin == "" {
			localHit = &b.Decisions[i]
		} else {
			peerHit = &b.Decisions[i]
		}
	}
	if localHit == nil {
		t.Fatal("the LOCAL ADR-G vanished — a same-id peer object must never displace it")
	}
	if peerHit == nil {
		t.Fatal("the PEER ADR-G vanished — identity is (origin, id), so both must survive")
	}
	if !strings.Contains(localHit.Summary, "ADR-G") && strings.Contains(localHit.Summary, "PEER") {
		t.Errorf("the local item carries the peer's body: %q", localHit.Summary)
	}
	q := qualified(b.Decisions)
	if !q["ADR-G"] || !q["platform:ADR-0001"] && !q["platform:ADR-G"] {
		t.Errorf("both ids must render distinctly: %v", q)
	}
}

// THE LOUD-FAILURE TEST. A peer object declaring `supersedes: ADR-G` must never
// supersede the LOCAL ADR-G. If store isolation ever breaks, the local decision
// silently drops to `superseded` and stops being served as live context — the
// highest-risk defect in federation, and invisible without this assertion.
func TestPeerSupersedesNeverTouchesSameIDLocalObject(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/sup.md": supersedingObj("ADR-PEERSUP", "decision", "global", "ADR-G",
			"the peer's replacement for ITS OWN ADR-G, about decisions"),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	var localG *Item
	for i := range b.Decisions {
		if b.Decisions[i].ID == "ADR-G" && b.Decisions[i].Origin == "" {
			localG = &b.Decisions[i]
		}
	}
	if localG == nil {
		t.Fatal("the local ADR-G disappeared from the bundle entirely — a foreign supersedes reached across stores")
	}
	if localG.Status == "superseded" {
		t.Fatalf("STORE ISOLATION BROKEN: a peer's `supersedes: ADR-G` superseded the LOCAL ADR-G. "+
			"Edges must resolve only within their own store (ADR-0032). status=%q", localG.Status)
	}
}

// The same guarantee for `amends`: a peer amendment must never annotate a
// same-id local object as partially overridden.
func TestPeerAmendsNeverTouchesSameIDLocalObject(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/am.md": statusObj("ADR-PEERAM", "decision", "global", "accepted",
			"the peer amendment, about decisions", "amends:ADR-G"),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range b.Decisions {
		if d.ID == "ADR-G" && d.Origin == "" && len(d.AmendedBy) > 0 {
			t.Fatalf("STORE ISOLATION BROKEN: a peer's `amends:ADR-G` annotated the LOCAL ADR-G as amended by %v", d.AmendedBy)
		}
	}
}

// A peer's one-hop `relates_to` must pull the PEER's target, never the local
// object that shares the id.
//
// The target is ADR-U, which is util-scoped LOCALLY and therefore out of scope
// for a render path: if the traversal ever resolved a foreign edge against the
// local store, the local util decision would appear in a render bundle it has
// no business being in — an observable, loud leak.
func TestPeerRelatesToResolvesWithinItsOwnStore(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/seed.md": statusObj("ADR-PEERSEED", "decision", "global", "accepted",
			"a peer decision about decisions", "prevents:ADR-U"),
		".nugit/decisions/u.md": statusObj("ADR-U", "decision", "global", "accepted",
			"the PEER's own ADR-U target", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range b.Decisions {
		if d.ID == "ADR-U" && d.Origin == "" {
			t.Fatalf("STORE ISOLATION BROKEN: a peer's `prevents:ADR-U` pulled the LOCAL, out-of-scope ADR-U into a render bundle (via %q)", d.Via)
		}
	}
	if _, ok := originOf(b.Decisions, "ADR-U"); !ok {
		t.Error("the PEER's own ADR-U should be reachable from its own store")
	}
	// The via label itself must name the peer seed, qualified.
	for _, d := range b.Decisions {
		if d.Via != "" && strings.Contains(d.Via, "ADR-PEERSEED") && !strings.Contains(d.Via, "platform:") {
			t.Errorf("via must qualify a foreign source: %q", d.Via)
		}
	}
}

// A peer object's applies_to_paths globs address the PEER's checkout. They must
// never path-bind a local file just because the two repos share a layout.
func TestPeerAppliesToPathsNeverBindsLocally(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/bound.md": "---\nschema_version: 1\nid: ADR-PEERBOUND\ntype: decision\nscope: render\n" +
			"status: accepted\ncreated: 2026-01-01T00:00:00Z\napplies_to_paths:\n  - \"internal/render/**\"\n" +
			"provenance:\n  commit: x\n---\n\n# ADR-PEERBOUND\n\npeer path-bound decision\n",
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := originOf(b.Decisions, "ADR-PEERBOUND"); ok {
		t.Error("a peer's applies_to_paths glob must not bind a LOCAL path (component-scoped foreign knowledge stays out)")
	}
}

// ---- ranking + budget ----

// Local always outranks peer, unconditionally: a bundle is read as THIS repo's
// context, so another repo's word never sorts above ours.
func TestLocalOutranksPeerInSort(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/a.md": statusObj("ADR-AAAA", "decision", "global", "accepted",
			"a peer decision about decisions, sorting first alphabetically", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	seenPeer := false
	for _, d := range b.Decisions {
		if d.Origin != "" {
			seenPeer = true
			continue
		}
		if seenPeer {
			t.Fatalf("local decision %s sorted AFTER a peer item — local must always outrank peer: %v",
				d.ID, qualified(b.Decisions))
		}
	}
	if !seenPeer {
		t.Fatal("fixture broken: the peer decision never made the bundle")
	}
}

// Under a tight budget, peer items are dropped before local items — and each
// drop is reported in Dropped[] like every other cut, qualified.
func TestPeerItemsDroppedBeforeLocalUnderTightBudget(t *testing.T) {
	local, _ := federated(t, map[string]string{
		".nugit/decisions/p1.md": statusObj("ADR-P1", "decision", "global", "accepted",
			"a peer decision about decisions with a reasonably long body to consume tokens", ""),
		".nugit/lessons/p2.md": statusObj("LESSON-P2", "lesson", "global", "accepted",
			"a peer lesson about decisions with a reasonably long body to consume tokens", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "decision", BudgetTokens: 70})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Truncated {
		t.Fatal("fixture broken: budget was not tight enough to truncate")
	}
	for _, d := range append(append([]Item{}, b.Decisions...), b.Lessons...) {
		if d.Origin != "" {
			t.Errorf("a peer item (%s) survived a budget that dropped local items", d.QualifiedID())
		}
	}
	joined := strings.Join(b.Dropped, "; ")
	if !strings.Contains(joined, "platform:ADR-P1") {
		t.Errorf("a dropped peer item must appear in Dropped[] with its qualified id, got %q", joined)
	}
	if len(b.Decisions) == 0 {
		t.Error("local decisions must survive a budget that drops peer items")
	}
}

// ---- degradation: an absent peer is normal, never an error ----

// CI checks out one repo. A configured peer that isn't there contributes
// nothing, errors nothing, and says so.
func TestMissingPeerPathDegradesToEmpty(t *testing.T) {
	local := setup(t)
	wf(t, local, ".nugit/config.yml",
		"schema_version: 1\npeers:\n  - name: platform\n    path: "+filepath.Join(t.TempDir(), "not-checked-out")+"\n")
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatalf("an absent peer must NEVER error: %v", err)
	}
	if len(b.Peers) != 1 || b.Peers[0].Reachable || b.Peers[0].Count != 0 {
		t.Fatalf("absent peer must report unreachable + zero objects, got %+v", b.Peers)
	}
	if b.Peers[0].Note == "" {
		t.Error("an unreachable peer must explain itself")
	}
	for _, d := range b.Decisions {
		if d.Origin != "" {
			t.Errorf("no foreign item may appear when the peer is absent: %s", d.QualifiedID())
		}
	}
	if !strings.Contains(b.Markdown(), "unreachable") {
		t.Error("the render must state that a configured peer contributed nothing")
	}
	// The local bundle is otherwise untouched.
	if !ids(b.Decisions)["ADR-R"] {
		t.Error("local knowledge must be unaffected by an absent peer")
	}
}

// A relative peer path resolves against the nugit root (the documented form).
func TestRelativePeerPathResolvesAgainstRepoRoot(t *testing.T) {
	parent := t.TempDir()
	local := filepath.Join(parent, "app-repo")
	peer := filepath.Join(parent, "platform-repo")
	wf(t, local, ".nugit/architecture/workspace.dsl", `workspace "m" {
  model { sys = softwareSystem "m" {
    render = component "R" { properties { paths "internal/render/**" } }
  } }
}`)
	wf(t, local, ".nugit/config.yml", "schema_version: 1\npeers:\n  - name: platform\n    path: ../platform-repo\n")
	wf(t, peer, ".nugit/decisions/g.md", statusObj("ADR-REL", "decision", "global", "accepted",
		"a peer decision about rendering", ""))
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "rendering"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := originOf(b.Decisions, "ADR-REL"); !ok {
		t.Errorf("relative peer path did not resolve: %+v", b.Peers)
	}
}

// One level only: a peer's OWN peers are never loaded. This is the cycle guard —
// A -> B -> A terminates because B's peers are not followed.
func TestPeersAreNotLoadedTransitively(t *testing.T) {
	grandpeer := t.TempDir()
	wf(t, grandpeer, ".nugit/decisions/g.md", statusObj("ADR-GRAND", "decision", "global", "accepted",
		"a grand-peer decision about rendering", ""))
	local, peer := federated(t, map[string]string{
		".nugit/decisions/p.md": statusObj("ADR-PEER", "decision", "global", "accepted",
			"a peer decision about rendering", ""),
	})
	wf(t, peer, ".nugit/config.yml", "schema_version: 1\npeers:\n  - name: deeper\n    path: "+grandpeer+"\n")
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "rendering"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := originOf(b.Decisions, "ADR-PEER"); !ok {
		t.Fatal("fixture broken: the direct peer contributed nothing")
	}
	if _, ok := originOf(b.Decisions, "ADR-GRAND"); ok {
		t.Error("a peer's OWN peers must never be loaded — federation is one level deep (the cycle guard)")
	}
}

// A peer pointing back at this repo would duplicate the whole local store under
// a foreign namespace. Fail closed, visibly.
func TestSelfPeerIsSkipped(t *testing.T) {
	local := setup(t)
	wf(t, local, ".nugit/config.yml", "schema_version: 1\npeers:\n  - name: self\n    path: .\n")
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "decision"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range b.Decisions {
		if d.Origin != "" {
			t.Errorf("a self-referential peer must contribute nothing, got %s", d.QualifiedID())
		}
	}
}

// ---- evidence ----

// A foreign object caps at `declared`, whatever tier it holds at home: this repo
// enforces nothing about it and mechanically verifies nothing.
func TestPeerEvidenceCapsAtDeclared(t *testing.T) {
	local, peer := federated(t, map[string]string{
		".nugit/decisions/g.md": statusObj("ADR-TIER", "decision", "global", "accepted",
			"a peer decision about rendering", "constrains:render"),
	})
	// Give the peer everything that would earn `enforced` at home: a Go module,
	// a path-bound component the object constrains, enforce mode.
	wf(t, peer, "go.mod", "module example.com/peer\n\ngo 1.25\n")
	wf(t, peer, ".nugit/architecture/workspace.dsl", `workspace "p" {
  model { sys = softwareSystem "p" {
    render = component "R" { properties { paths "internal/render/**" } }
  } }
}`)
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go", Task: "rendering"})
	if err != nil {
		t.Fatal(err)
	}
	var it *Item
	for i := range b.Decisions {
		if b.Decisions[i].ID == "ADR-TIER" {
			it = &b.Decisions[i]
		}
	}
	if it == nil {
		t.Fatal("fixture broken: the peer decision never made the bundle")
	}
	if it.Tier != "declared" {
		t.Errorf("foreign tier = %q, want declared — this repo verifies nothing about a peer's record", it.Tier)
	}
}
