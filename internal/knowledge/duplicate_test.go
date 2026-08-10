package knowledge

// Tests for the ADR-0039 duplicate-id core: two objects in ONE store carrying
// the same id are a defect; the SAME id in a peer store is the federated norm
// and must never be reported (ADR-0032).

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func at(id, path string) model.KnowledgeObject {
	return model.KnowledgeObject{
		FrontMatter: model.FrontMatter{ID: id, Type: model.KindDecision, Status: model.StatusAccepted},
		Path:        path,
	}
}

func TestDuplicateIDsNamesEveryFile(t *testing.T) {
	objs := []model.KnowledgeObject{
		at("ADR-P-0007", ".nugit/decisions/0007-second.md"),
		at("ADR-P-0007", ".nugit/decisions/0007-first.md"),
		at("ADR-P-0008", ".nugit/decisions/0008-alone.md"),
	}
	got := DuplicateIDs(objs)
	if len(got) != 1 {
		t.Fatalf("want exactly the one collision, got %+v", got)
	}
	if got[0].ID != "ADR-P-0007" || got[0].Origin != "" {
		t.Errorf("id/origin = %q/%q, want ADR-P-0007/local", got[0].ID, got[0].Origin)
	}
	// Every file, sorted — a finding a reader has to go searching from is not
	// actionable.
	want := []string{".nugit/decisions/0007-first.md", ".nugit/decisions/0007-second.md"}
	if strings.Join(got[0].Paths, ",") != strings.Join(want, ",") {
		t.Errorf("paths = %v, want %v", got[0].Paths, want)
	}
}

func TestDuplicateIDsSilentWhenUnique(t *testing.T) {
	objs := []model.KnowledgeObject{
		at("ADR-P-0007", ".nugit/decisions/a.md"),
		at("ADR-P-0008", ".nugit/decisions/b.md"),
		at("LESSON-x", ".nugit/lessons/x.md"),
	}
	if got := DuplicateIDs(objs); len(got) != 0 {
		t.Fatalf("unique ids must stay silent, got %+v", got)
	}
}

// THE FEDERATION CASE. Every repo mints ADR-0001, so the same id in a peer
// store is expected and correct — identity is (origin, id). Flagging it would
// make federation itself look like a defect.
func TestDuplicateIDsNeverFlagsPeerReuse(t *testing.T) {
	local := at("ADR-0001", ".nugit/decisions/0001-local.md")
	peer := at("ADR-0001", ".nugit/decisions/0001-theirs.md")
	peer.Origin = "platform"
	other := at("ADR-0001", ".nugit/decisions/0001-third.md")
	other.Origin = "edge"

	if got := DuplicateIDs([]model.KnowledgeObject{local, peer, other}); len(got) != 0 {
		t.Fatalf("CROSS-STORE ID REUSE IS LEGITIMATE (ADR-0032): every repo mints ADR-0001, "+
			"and a peer's copy is a different object under a different key. Got %+v", got)
	}
}

// …but a collision INSIDE a peer's own store is still a duplicate, reported
// under that peer's origin and qualified for display.
func TestDuplicateIDsWithinOnePeerStore(t *testing.T) {
	local := at("ADR-0001", ".nugit/decisions/0001-local.md")
	a := at("ADR-0001", ".nugit/decisions/a.md")
	a.Origin = "platform"
	b := at("ADR-0001", ".nugit/decisions/b.md")
	b.Origin = "platform"

	got := DuplicateIDs([]model.KnowledgeObject{local, a, b})
	if len(got) != 1 {
		t.Fatalf("want the peer-internal collision only, got %+v", got)
	}
	if got[0].Origin != "platform" || got[0].QualifiedID() != "platform:ADR-0001" {
		t.Errorf("origin/qualified = %q/%q, want platform/platform:ADR-0001", got[0].Origin, got[0].QualifiedID())
	}
}

// An id-less object (malformed front-matter — doctor's untyped check owns it)
// must not crash the grouping, and must not make every untyped file a
// "duplicate" of every other.
func TestDuplicateIDsIgnoresEmptyIDs(t *testing.T) {
	objs := []model.KnowledgeObject{
		{Path: ".nugit/decisions/bad-a.md"},
		{Path: ".nugit/decisions/bad-b.md"},
		{Path: ".nugit/decisions/bad-c.md"},
		at("ADR-P-0007", ".nugit/decisions/ok.md"),
	}
	if got := DuplicateIDs(objs); len(got) != 0 {
		t.Fatalf("id-less objects must not group, got %+v", got)
	}
}

// Ordering is deterministic: local before peer, then by id.
func TestDuplicateIDsSortedDeterministically(t *testing.T) {
	mk := func(id, origin, path string) model.KnowledgeObject {
		o := at(id, path)
		o.Origin = origin
		return o
	}
	objs := []model.KnowledgeObject{
		mk("ADR-9", "platform", "p/b.md"), mk("ADR-9", "platform", "p/a.md"),
		mk("ADR-2", "", "l/b.md"), mk("ADR-2", "", "l/a.md"),
		mk("ADR-1", "", "l/d.md"), mk("ADR-1", "", "l/c.md"),
	}
	got := DuplicateIDs(objs)
	var order []string
	for _, d := range got {
		order = append(order, d.QualifiedID())
	}
	want := "ADR-1,ADR-2,platform:ADR-9"
	if strings.Join(order, ",") != want {
		t.Errorf("order = %v, want %s", order, want)
	}
}

// End-to-end over a real store on disk: two files, one id.
func TestDuplicateIDsFromLoadedStore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nugit/decisions/0007-accepted.md", record("ADR-P-0007", "decision", "accepted", ""))
	writeFile(t, dir, ".nugit/decisions/0007-proposed.md", record("ADR-P-0007", "decision", "proposed", ""))
	writeFile(t, dir, ".nugit/decisions/0008-fine.md", record("ADR-P-0008", "decision", "accepted", ""))

	objs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := DuplicateIDs(objs)
	if len(got) != 1 || len(got[0].Paths) != 2 {
		t.Fatalf("want one collision over two files, got %+v", got)
	}
	for _, p := range []string{".nugit/decisions/0007-accepted.md", ".nugit/decisions/0007-proposed.md"} {
		if !strings.Contains(strings.Join(got[0].Paths, ","), p) {
			t.Errorf("paths %v must name %s", got[0].Paths, p)
		}
	}
}
