package beads

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/gitutil"
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
	pos := Position(storeOf(issues), nil)
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

// storeOf builds an in-memory store the way Read would, so tests exercise the
// same plan assignment the reader uses.
func storeOf(issues []Issue) Store {
	const f = ".beads/issues.jsonl"
	for i := range issues {
		issues[i].File = f
	}
	st := Store{Issues: issues, Files: []string{f},
		Stats: map[string]FileStat{f: {Lines: len(issues), Parsed: len(issues)}}}
	assignPlans(&st)
	return st
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// PlanPosition reads the store at the reviewed REF, never the working tree.
func TestPlanPositionAtRef(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	repo := gitutil.Repo{Dir: dir}

	// no store committed -> degrade (ok=false)
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "base")
	if _, ok := PlanPosition(repo, "HEAD", ""); ok {
		t.Error("no committed store -> ok=false")
	}

	// commit a store
	os.MkdirAll(filepath.Join(dir, ".beads"), 0o755)
	os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"),
		[]byte(`{"id":"bd-1","title":"E","issue_type":"epic","status":"open"}`+"\n"), 0o644)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "add beads")

	pos, ok := PlanPosition(repo, "HEAD", "")
	if !ok || !pos.Present || len(pos.Remaining) != 1 {
		t.Fatalf("expected one remaining epic at HEAD, got ok=%v pos=%+v", ok, pos)
	}

	// an UNCOMMITTED working-tree edit must NOT change the result read at HEAD
	os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"),
		[]byte(`{"id":"bd-1","title":"E","issue_type":"epic","status":"closed"}`+"\n"), 0o644)
	pos2, _ := PlanPosition(repo, "HEAD", "")
	if len(pos2.Completed) != 0 || len(pos2.Remaining) != 1 {
		t.Errorf("working-tree edit leaked into the ref read: %+v", pos2)
	}
}

func TestDedupByIDLastWins(t *testing.T) {
	issues := []Issue{
		{ID: "bd-1", Title: "old", Status: "open", Type: "epic"},
		{ID: "bd-1", Title: "new", Status: "closed", Type: "epic"},
	}
	pos := Position(storeOf(dedupByID(issues)), nil)
	if len(pos.Completed) != 1 || len(pos.Remaining) != 0 {
		t.Errorf("duplicate id not collapsed last-wins: %+v", pos)
	}
}

func TestNumericIDAndTrim(t *testing.T) {
	is := ParseJSONL([]byte(`{"id":42,"title":"x","issue_type":" Epic ","status":" Closed "}`))
	if len(is) != 1 || is[0].ID != "42" {
		t.Fatalf("numeric id not coerced: %+v", is)
	}
	if is[0].Type != "epic" || is[0].Status != "closed" {
		t.Errorf("type/status not trimmed+lowered: %+v", is[0])
	}
}
