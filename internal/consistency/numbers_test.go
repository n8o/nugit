package consistency

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// A PR that changes a record's id and not its file name leaves two decisions
// on one number. The ids differ, so duplicate-knowledge-id is silent.
func TestDuplicateNumberFailsWhenPRTouchesIt(t *testing.T) {
	kept := dupObj("ADR-0055", ".nugit/decisions/0055-a-change.md")
	moved := dupObj("ADR-0063", ".nugit/decisions/0055-task-words.md")

	in := lifecycleInput([]model.KnowledgeObject{kept, moved},
		model.KnowledgeChange{Path: moved.Path, Status: "M", Object: &moved})

	if fs := checkDuplicateID(in); len(fs) != 0 {
		t.Fatalf("the ids differ, so the id check must stay silent: %+v", fs)
	}
	fs := checkDuplicateNumber(in)
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
	f := fs[0]
	if f.Check != "duplicate-decision-number" || f.Severity != model.SevFail {
		t.Errorf("got %s/%s, want duplicate-decision-number/fail", f.Check, f.Severity)
	}
	for _, want := range []string{"0055-a-change.md", "0055-task-words.md", "ADR-0063"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail must name %s: %s", want, f.Detail)
		}
	}
}

// A shared number the PR never touches is old drift and doctor's job, as for
// every store-wide check here.
func TestDuplicateNumberIgnoresUntouchedRecords(t *testing.T) {
	a := dupObj("ADR-0055", ".nugit/decisions/0055-a.md")
	b := dupObj("ADR-0063", ".nugit/decisions/0055-b.md")
	other := dupObj("ADR-0070", ".nugit/decisions/0070-other.md")

	in := lifecycleInput([]model.KnowledgeObject{a, b, other},
		model.KnowledgeChange{Path: other.Path, Status: "A", Object: &other})
	if fs := checkDuplicateNumber(in); len(fs) != 0 {
		t.Errorf("the PR touched neither 0055 record: %+v", fs)
	}
}

// The sibling's 0063 landed after this branch was cut. Only the target's tip
// shows it.
func TestNumberTakenOnTargetFails(t *testing.T) {
	mine := dupObj("ADR-0063", ".nugit/decisions/0063-mine.md")
	in := lifecycleInput([]model.KnowledgeObject{mine},
		model.KnowledgeChange{Path: mine.Path, Status: "A", Object: &mine})
	in.TargetRef = "main"
	in.TargetIDs = map[string][]string{"ADR-0063-B": {".nugit/decisions/0063-theirs.md"}}

	// A different namespace on the same number is not a collision.
	if fs := checkNumberTakenOnTarget(in); len(fs) != 0 {
		t.Fatalf("ADR-0063-B is another namespace: %+v", fs)
	}

	in.TargetIDs = map[string][]string{"ADR-0064": {".nugit/decisions/0063-theirs.md"}}
	fs := checkNumberTakenOnTarget(in)
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %+v", fs)
	}
	if fs[0].Check != "duplicate-decision-number" || fs[0].Severity != model.SevFail ||
		!strings.Contains(fs[0].Title, "already taken on main") || !strings.Contains(fs[0].Detail, "0063-theirs.md") {
		t.Errorf("got %+v", fs[0])
	}

	// The same id on the target is checkIDTakenOnTarget's finding, not a second one.
	in.TargetIDs = map[string][]string{"ADR-0063": {".nugit/decisions/0063-theirs.md"}}
	if fs := checkNumberTakenOnTarget(in); len(fs) != 0 {
		t.Errorf("a shared id is reported by the id check: %+v", fs)
	}
	// The same path on both sides is this PR's own record arriving twice.
	in.TargetIDs = map[string][]string{"ADR-0064": {mine.Path}}
	if fs := checkNumberTakenOnTarget(in); len(fs) != 0 {
		t.Errorf("the same path is not a rival: %+v", fs)
	}
}

func TestDuplicateNumberHasAnExplainEntry(t *testing.T) {
	s, ok := Explain("duplicate-decision-number")
	if !ok || !strings.Contains(s, "ADR-0041") {
		t.Fatalf("duplicate-decision-number needs an explain entry naming its reference point: %q", s)
	}
}
