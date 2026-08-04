package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func record(id, typ, status, extra string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: " + typ + "\nscope: global\nstatus: " + status +
		"\ncreated: 2026-01-01T00:00:00Z\n" + extra + "provenance:\n  commit: x\n---\n\n# " + id + "\n\nbody\n"
}

// ---- the loud-failure guards, exercised on a MERGED slice ----
//
// Retrieval resolves each store before merging, so these assertions are defense
// in depth: they hold even if a future caller hands a merged set straight to the
// resolvers. That is the invariant ADR-0032 actually promises — identity is
// (origin, id) INSIDE the resolvers, not only in the call order around them.

// A foreign `supersedes: ADR-0007` must never supersede the LOCAL ADR-0007.
// This is the highest-risk defect in federation: the local decision silently
// drops to `superseded` and stops being served as live context.
func TestSupersedesNeverCrossesStores(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "ADR-0007", Type: model.KindDecision, Status: model.StatusAccepted}},
		{FrontMatter: model.FrontMatter{ID: "ADR-0009", Type: model.KindDecision, Status: model.StatusAccepted,
			Supersedes: "ADR-0007"}, Origin: "platform"},
	}
	ResolveEffectiveStatus(objs)
	if objs[0].EffectiveStatus == model.StatusSuperseded {
		t.Fatal("STORE ISOLATION BROKEN: a peer's `supersedes: ADR-0007` superseded the LOCAL ADR-0007. " +
			"Supersession must resolve only within its own store (ADR-0032).")
	}
	if objs[0].EffectiveStatus != model.StatusAccepted {
		t.Errorf("local status = %q, want accepted", objs[0].EffectiveStatus)
	}
}

// The mirror case: a LOCAL supersedes must not reach across into a peer's
// same-id record either. We must not mutate a peer, even derivationally.
func TestLocalSupersedesDoesNotReachIntoPeer(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "ADR-0007", Type: model.KindDecision, Status: model.StatusAccepted},
			Origin: "platform"},
		{FrontMatter: model.FrontMatter{ID: "ADR-0009", Type: model.KindDecision, Status: model.StatusAccepted,
			Supersedes: "ADR-0007"}},
	}
	ResolveEffectiveStatus(objs)
	if objs[0].EffectiveStatus == model.StatusSuperseded {
		t.Fatal("STORE ISOLATION BROKEN: a LOCAL supersedes killed the PEER's same-id record")
	}
}

// A peer's supersedes DOES apply within the peer's own store — isolation must
// not become inertness.
func TestSupersedesStillWorksWithinAPeerStore(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "ADR-0007", Type: model.KindDecision, Status: model.StatusAccepted},
			Origin: "platform"},
		{FrontMatter: model.FrontMatter{ID: "ADR-0009", Type: model.KindDecision, Status: model.StatusAccepted,
			Supersedes: "ADR-0007"}, Origin: "platform"},
	}
	ResolveEffectiveStatus(objs)
	if objs[0].EffectiveStatus != model.StatusSuperseded {
		t.Errorf("a peer's own supersedes must still resolve inside its store, got %q", objs[0].EffectiveStatus)
	}
}

// `amends` is the same rule: a peer amendment must never annotate a same-id
// local record as partially overridden.
func TestAmendsNeverCrossesStores(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "ADR-0007", Type: model.KindDecision, Status: model.StatusAccepted}},
		{FrontMatter: model.FrontMatter{ID: "ADR-0009", Type: model.KindDecision, Status: model.StatusAccepted,
			RelatesTo: []string{"amends:ADR-0007"}}, Origin: "platform"},
	}
	ResolveEffectiveStatus(objs)
	ResolveAmendedBy(objs)
	if len(objs[0].AmendedBy) > 0 {
		t.Fatalf("STORE ISOLATION BROKEN: a peer's `amends:ADR-0007` annotated the LOCAL ADR-0007 (%v)", objs[0].AmendedBy)
	}
}

// …and `reinforces`: a peer must not widen a local record's retrieval surface.
func TestReinforcesNeverCrossesStores(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "LESSON-x", Type: model.KindLesson, Status: model.StatusAccepted}},
		{FrontMatter: model.FrontMatter{ID: "LESSON-y", Type: model.KindLesson, Status: model.StatusAccepted,
			RelatesTo: []string{"reinforces:LESSON-x"}}, Origin: "platform"},
	}
	ResolveEffectiveStatus(objs)
	ResolveReinforcedBy(objs)
	if len(objs[0].ReinforcedBy) > 0 {
		t.Fatalf("STORE ISOLATION BROKEN: a peer reinforced the LOCAL LESSON-x (%v)", objs[0].ReinforcedBy)
	}
}

// Annotations that DO resolve name their source qualified, so a local reader
// can tell whose amendment it is.
func TestWithinPeerAnnotationsAreQualified(t *testing.T) {
	objs := []model.KnowledgeObject{
		{FrontMatter: model.FrontMatter{ID: "ADR-0007", Type: model.KindDecision, Status: model.StatusAccepted},
			Origin: "platform"},
		{FrontMatter: model.FrontMatter{ID: "ADR-0009", Type: model.KindDecision, Status: model.StatusAccepted,
			RelatesTo: []string{"amends:ADR-0007"}}, Origin: "platform"},
	}
	ResolveEffectiveStatus(objs)
	ResolveAmendedBy(objs)
	if len(objs[0].AmendedBy) != 1 || objs[0].AmendedBy[0] != "platform:ADR-0009" {
		t.Errorf("amended_by = %v, want [platform:ADR-0009]", objs[0].AmendedBy)
	}
}

// ---- loading ----

func TestLoadPeerStampsOriginAndIsReadOnly(t *testing.T) {
	peer := t.TempDir()
	writeFile(t, peer, ".nugit/decisions/a.md", record("ADR-0001", "decision", "accepted", ""))
	objs, load := LoadPeer(PeerSource{Name: "platform", Dir: peer})
	if !load.Reachable || load.Count != 1 {
		t.Fatalf("load = %+v, want reachable with 1 object", load)
	}
	if len(objs) != 1 || objs[0].Origin != "platform" {
		t.Fatalf("origin not stamped: %+v", objs)
	}
	if !objs[0].Foreign() || objs[0].QualifiedID() != "platform:ADR-0001" {
		t.Errorf("qualified id = %q, want platform:ADR-0001", objs[0].QualifiedID())
	}
	// Read-only: nothing under the peer changed. The on-disk id is untouched —
	// rewriting it would break ADR-0001 stable keys and dangle the peer's own
	// references.
	b, err := os.ReadFile(filepath.Join(peer, ".nugit/decisions/a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != record("ADR-0001", "decision", "accepted", "") {
		t.Error("the peer's file was modified — a peer store is never written")
	}
}

func TestLoadPeerMissingPathIsNotAnError(t *testing.T) {
	objs, load := LoadPeer(PeerSource{Name: "platform", Dir: filepath.Join(t.TempDir(), "absent")})
	if len(objs) != 0 {
		t.Errorf("an absent peer must contribute nothing, got %d", len(objs))
	}
	if load.Reachable {
		t.Error("an absent peer must report unreachable")
	}
	if load.Note == "" {
		t.Error("an absent peer must explain itself — silence is indistinguishable from an empty store")
	}
}

func TestLoadWithPeersMergesAndReportsEachPeer(t *testing.T) {
	local := t.TempDir()
	writeFile(t, local, ".nugit/decisions/a.md", record("ADR-0001", "decision", "accepted", ""))
	peer := t.TempDir()
	writeFile(t, peer, ".nugit/decisions/a.md", record("ADR-0001", "decision", "accepted", ""))

	objs, loads, err := LoadWithPeers(local, []PeerSource{
		{Name: "platform", Dir: peer},
		{Name: "absent", Dir: filepath.Join(t.TempDir(), "nope")},
	})
	if err != nil {
		t.Fatalf("an absent peer must never error the load: %v", err)
	}
	// Both ADR-0001s survive: identity is (origin, id).
	if len(objs) != 2 {
		t.Fatalf("want 2 objects (one per store), got %d", len(objs))
	}
	if objs[0].Origin != "" || objs[1].Origin != "platform" {
		t.Errorf("origins = %q,%q; local must come first, unstamped", objs[0].Origin, objs[1].Origin)
	}
	if got := PeerOrigins(objs); len(got) != 1 || got[0] != "platform" {
		t.Errorf("PeerOrigins = %v, want [platform]", got)
	}
	if len(loads) != 2 || !loads[0].Reachable || loads[1].Reachable {
		t.Errorf("loads = %+v, want platform reachable and absent not", loads)
	}
}

// Load stays local: every WRITER (ratify, reinforce, distill, the projections)
// must keep seeing exactly this repo's store — ADR-0011's single writer.
func TestLoadStaysLocalOnly(t *testing.T) {
	local := t.TempDir()
	writeFile(t, local, ".nugit/decisions/a.md", record("ADR-0001", "decision", "accepted", ""))
	objs, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if o.Foreign() {
			t.Errorf("knowledge.Load must never return a foreign object, got %s", o.QualifiedID())
		}
	}
}
