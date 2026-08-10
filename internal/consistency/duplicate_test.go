package consistency

// Tests for the ADR-0039 duplicate-knowledge-id check: a collision introduced
// by this PR fails at the reviewed ref; pre-existing drift the PR never touched
// stays doctor's job.

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func dupObj(id, path string) model.KnowledgeObject {
	o := model.KnowledgeObject{
		FrontMatter: model.FrontMatter{ID: id, Type: model.KindDecision, Status: model.StatusAccepted},
		Path:        path,
	}
	o.EffectiveStatus = o.Status
	return o
}

func TestDuplicateIDFailsWhenPRIntroducesIt(t *testing.T) {
	old := dupObj("ADR-P-0027", ".nugit/decisions/0027-first.md")
	newer := dupObj("ADR-P-0027", ".nugit/decisions/0027-second.md")
	newer.Status = model.StatusProposed
	newer.EffectiveStatus = model.StatusProposed

	in := lifecycleInput([]model.KnowledgeObject{old, newer},
		model.KnowledgeChange{Path: newer.Path, Status: "A", Object: &newer})

	fs := checkDuplicateID(in)
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
	f := fs[0]
	if f.Check != "duplicate-knowledge-id" {
		t.Errorf("check = %q", f.Check)
	}
	if f.Severity != model.SevFail {
		t.Errorf("severity = %q, want fail — an exact grouping over committed text has no false positives to be advisory about (ADR-0039)", f.Severity)
	}
	if !strings.Contains(f.Title, "ADR-P-0027") {
		t.Errorf("title must name the id, got %q", f.Title)
	}
	for _, p := range []string{".nugit/decisions/0027-first.md", ".nugit/decisions/0027-second.md"} {
		if !strings.Contains(f.Detail, p) {
			t.Errorf("detail must name %s, got %q", p, f.Detail)
		}
	}
}

// Modifying the OTHER half of an existing collision fires too: the PR is
// editing a record whose identity is ambiguous, and that is the moment to fix it.
func TestDuplicateIDFiresWhenEitherHalfIsTouched(t *testing.T) {
	a := dupObj("ADR-P-0027", ".nugit/decisions/a.md")
	b := dupObj("ADR-P-0027", ".nugit/decisions/b.md")
	in := lifecycleInput([]model.KnowledgeObject{a, b},
		model.KnowledgeChange{Path: a.Path, Status: "M", Object: &a})

	if fs := checkDuplicateID(in); len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
}

// Pre-existing store drift the PR never touched is doctor's job — pr-render
// must not fail every unrelated PR on it.
func TestDuplicateIDScopedToTouchedObjects(t *testing.T) {
	a := dupObj("ADR-P-0027", ".nugit/decisions/a.md")
	b := dupObj("ADR-P-0027", ".nugit/decisions/b.md")
	other := dupObj("LESSON-x", ".nugit/lessons/x.md")

	in := lifecycleInput([]model.KnowledgeObject{a, b, other},
		model.KnowledgeChange{Path: other.Path, Status: "A", Object: &other})
	if fs := checkDuplicateID(in); len(fs) != 0 {
		t.Fatalf("untouched collision must stay silent in pr-render, got %+v", fs)
	}
	// No knowledge delta at all: nothing runs.
	if fs := checkDuplicateID(lifecycleInput([]model.KnowledgeObject{a, b})); len(fs) != 0 {
		t.Fatalf("no knowledge delta: must stay silent, got %+v", fs)
	}
}

func TestDuplicateIDSilentWhenIDsAreUnique(t *testing.T) {
	a := dupObj("ADR-P-0027", ".nugit/decisions/a.md")
	b := dupObj("ADR-P-0028", ".nugit/decisions/b.md")
	in := lifecycleInput([]model.KnowledgeObject{a, b},
		model.KnowledgeChange{Path: a.Path, Status: "A", Object: &a})

	if fs := checkDuplicateID(in); len(fs) != 0 {
		t.Fatalf("unique ids must stay silent, got %+v", fs)
	}
}

// The federation guard at the check surface: a peer's same-id record is not a
// duplicate (ADR-0032). pr-render never loads peers, so this pins the property
// against a future caller that hands a merged slice in.
func TestDuplicateIDNeverFlagsPeerReuse(t *testing.T) {
	local := dupObj("ADR-0001", ".nugit/decisions/0001-local.md")
	peer := dupObj("ADR-0001", ".nugit/decisions/0001-theirs.md")
	peer.Origin = "platform"

	in := lifecycleInput([]model.KnowledgeObject{local, peer},
		model.KnowledgeChange{Path: local.Path, Status: "M", Object: &local})
	if fs := checkDuplicateID(in); len(fs) != 0 {
		t.Fatalf("a peer minting the same id is legitimate (ADR-0032) and must never fail a PR, got %+v", fs)
	}
}

func TestDuplicateIDDeletedObjectIgnored(t *testing.T) {
	a := dupObj("ADR-P-0027", ".nugit/decisions/a.md")
	b := dupObj("ADR-P-0027", ".nugit/decisions/b.md")
	in := lifecycleInput([]model.KnowledgeObject{a, b},
		model.KnowledgeChange{Path: a.Path, Status: "D", Object: &a})

	if fs := checkDuplicateID(in); len(fs) != 0 {
		t.Fatalf("deleting a file is how a collision is RESOLVED, not introduced, got %+v", fs)
	}
}

func TestDuplicateIDHasExplanation(t *testing.T) {
	s, ok := Explain("duplicate-knowledge-id")
	if !ok {
		t.Fatal("duplicate-knowledge-id must have an explain entry")
	}
	if !strings.Contains(strings.ToLower(s), "peer store is legitimate") {
		t.Error("the explain text must say cross-store id reuse is legitimate — it is the subtlety most easily got wrong")
	}
}
