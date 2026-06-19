package beads

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseJSONLAndCompute(t *testing.T) {
	jsonl := strings.Join([]string{
		`{"id":"bd-1","title":"Auth epic","issue_type":"epic","status":"closed"}`,
		`{"id":"bd-2","title":"Billing epic","type":"epic","status":"in_progress"}`,
		`{"id":"bd-3","title":"Search epic","kind":"epic","state":"open"}`,
		`{"id":"bd-4","summary":"a task","issue_type":"task","status":"open"}`, // not an epic
		``,         // blank line tolerated
		`not json`, // garbage tolerated
	}, "\n")

	issues := ParseJSONL([]byte(jsonl))
	if len(issues) != 4 {
		t.Fatalf("want 4 parsed issues, got %d: %+v", len(issues), issues)
	}
	pos := compute(issues)
	if !pos.Present {
		t.Fatal("present should be true")
	}
	// epics present -> scope is epics only (the task is excluded)
	if len(pos.Completed) != 1 || !strings.Contains(pos.Completed[0], "Auth") {
		t.Errorf("completed = %v, want [Auth]", pos.Completed)
	}
	if !strings.Contains(pos.Current, "Billing") {
		t.Errorf("current = %q, want Billing", pos.Current)
	}
	if len(pos.Remaining) != 1 || !strings.Contains(pos.Remaining[0], "Search") {
		t.Errorf("remaining = %v, want [Search]", pos.Remaining)
	}
}

func TestDetectAndPlanPosition(t *testing.T) {
	dir := t.TempDir()
	if Detect(dir) {
		t.Error("no .beads dir should not detect")
	}
	if _, ok := PlanPosition(dir); ok {
		t.Error("no store -> ok=false (degrade to plan.yml)")
	}
	bd := filepath.Join(dir, ".beads")
	os.MkdirAll(bd, 0o755)
	os.WriteFile(filepath.Join(bd, "issues.jsonl"),
		[]byte(`{"id":"bd-1","title":"E","issue_type":"epic","status":"open"}`+"\n"), 0o644)
	if !Detect(dir) {
		t.Error("a .beads/*.jsonl should detect")
	}
	pos, ok := PlanPosition(dir)
	if !ok || !pos.Present || len(pos.Remaining) != 1 {
		t.Errorf("expected one remaining epic, got ok=%v pos=%+v", ok, pos)
	}
}
