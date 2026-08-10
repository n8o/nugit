package ratify

// Tests for ADR-0039 at the ratify surface: a duplicated id makes `nugit ratify
// <id>` undefined, so it must refuse and name both files rather than promote an
// arbitrary one — and `-list` must show the collision even when its other half
// is not a candidate.

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// The observed shape: two decision files a day apart, one accepted and one
// proposed, both carrying one id.
func TestRatifyRefusesDuplicateID(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/0027-accepted.md", obj("ADR-P-0027", "decision", "accepted"))
	wf(t, dir, ".nugit/decisions/0027-proposed.md", obj("ADR-P-0027", "decision", "proposed"))

	before := read(t, dir, ".nugit/decisions/0027-proposed.md")
	_, err := Ratify(dir, "ADR-P-0027")
	if err == nil {
		t.Fatal("an ambiguous id must refuse, not promote an arbitrary file")
	}
	for _, p := range []string{".nugit/decisions/0027-accepted.md", ".nugit/decisions/0027-proposed.md"} {
		if !strings.Contains(err.Error(), p) {
			t.Errorf("the error must name %s, got %q", p, err)
		}
	}
	// Nothing may be written on the refusal path.
	if read(t, dir, ".nugit/decisions/0027-proposed.md") != before {
		t.Error("a refused ratify must not touch any file")
	}
}

// Both halves proposed is the same refusal — "they are both candidates" does
// not make picking one defined.
func TestRatifyRefusesTwoProposedTwins(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/a.md", obj("ADR-P-0027", "decision", "proposed"))
	wf(t, dir, ".nugit/decisions/b.md", obj("ADR-P-0027", "decision", "proposed"))

	if _, err := Ratify(dir, "ADR-P-0027"); err == nil {
		t.Fatal("two proposed twins must still refuse")
	}
	if read(t, dir, ".nugit/decisions/a.md") != obj("ADR-P-0027", "decision", "proposed") {
		t.Error("no file may be mutated")
	}
}

// A unique id still ratifies — the refusal must not become a blanket block.
func TestRatifyUnaffectedByUnrelatedDuplicate(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/dup-a.md", obj("ADR-P-0027", "decision", "accepted"))
	wf(t, dir, ".nugit/decisions/dup-b.md", obj("ADR-P-0027", "decision", "proposed"))
	wf(t, dir, ".nugit/decisions/fine.md", obj("ADR-P-0028", "decision", "proposed"))

	res, err := Ratify(dir, "ADR-P-0028")
	if err != nil {
		t.Fatalf("an unrelated collision must not block a well-formed id: %v", err)
	}
	if res.To != model.StatusAccepted {
		t.Errorf("to = %q, want accepted", res.To)
	}
}

// `-list` hid the second object: the accepted twin is not a candidate, so the
// pending filter dropped it and the operator had no way to learn it existed.
func TestListSurfacesTheHiddenTwin(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/0027-accepted.md", obj("ADR-P-0027", "decision", "accepted"))
	wf(t, dir, ".nugit/decisions/0027-proposed.md", obj("ADR-P-0027", "decision", "proposed"))
	wf(t, dir, ".nugit/decisions/0028.md", obj("ADR-P-0028", "decision", "proposed"))

	l, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Duplicates) != 1 {
		t.Fatalf("want the collision reported, got %+v", l.Duplicates)
	}
	if l.Duplicates[0].ID != "ADR-P-0027" || len(l.Duplicates[0].Paths) != 2 {
		t.Fatalf("duplicate = %+v, want ADR-P-0027 over both files", l.Duplicates[0])
	}
	joined := strings.Join(l.Duplicates[0].Paths, ",")
	for _, p := range []string{".nugit/decisions/0027-accepted.md", ".nugit/decisions/0027-proposed.md"} {
		if !strings.Contains(joined, p) {
			t.Errorf("-list must name %s, got %v", p, l.Duplicates[0].Paths)
		}
	}
	// The candidate lane itself is unchanged.
	if len(l.Pending) != 2 {
		t.Errorf("want both proposed objects still listed, got %d", len(l.Pending))
	}
}

// Two proposed twins both appear as rows, ordered by path so they are
// distinguishable and the output is deterministic.
func TestListShowsBothProposedTwins(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/z.md", obj("ADR-P-0027", "decision", "proposed"))
	wf(t, dir, ".nugit/decisions/a.md", obj("ADR-P-0027", "decision", "proposed"))

	l, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Pending) != 2 {
		t.Fatalf("want both twins listed, got %d", len(l.Pending))
	}
	if l.Pending[0].Path != ".nugit/decisions/a.md" || l.Pending[1].Path != ".nugit/decisions/z.md" {
		t.Errorf("same-id rows must order by path, got %s then %s", l.Pending[0].Path, l.Pending[1].Path)
	}
}

func TestListCleanStoreReportsNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/decisions/a.md", obj("ADR-P-0027", "decision", "proposed"))
	wf(t, dir, ".nugit/decisions/b.md", obj("ADR-P-0028", "decision", "accepted"))
	// Malformed front-matter: id-less, must not read as a duplicate.
	wf(t, dir, ".nugit/decisions/bad.md", "---\nid: ADR-BAD\nsupersedes:\n  - ADR-X\n---\n\n# bad\n")

	l, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Duplicates) != 0 {
		t.Fatalf("clean store must report no collisions, got %+v", l.Duplicates)
	}
	if len(l.Pending) != 1 || l.Pending[0].ID != "ADR-P-0027" {
		t.Errorf("pending = %+v, want just ADR-P-0027", l.Pending)
	}
}
