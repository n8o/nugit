package delta

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
)

func gitP(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// store writes the plan store and commits it, returning the new ref.
func storeCommit(t *testing.T, dir, msg string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".beads", "issues.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitP(t, dir, "add", "-A")
	gitP(t, dir, "commit", "-q", "-m", msg)
}

// The failure this scoping exists to end: one repo-wide store, several plans in
// flight, and a PR that advances ONE of them rendering all of them as its own
// position.
func TestDiffPlanScopesToTheePlansThisPRMoved(t *testing.T) {
	dir := t.TempDir()
	gitP(t, dir, "init", "-q")
	gitP(t, dir, "checkout", "-q", "-b", "main")

	rift1 := `{"id":"p-rift-1","title":"rift one","issue_type":"epic","status":%s}`
	base := []string{
		strings.Replace(rift1, "%s", `"in_progress"`, 1),
		`{"id":"p-rift-2","title":"rift two","issue_type":"epic","status":"open"}`,
		`{"id":"p-flux-1","title":"flux one","issue_type":"epic","status":"in_progress"}`,
		`{"id":"p-flux-2","title":"flux two","issue_type":"epic","status":"open"}`,
	}
	storeCommit(t, dir, "base", base...)

	// This PR closes one rift step. The flux plan is untouched — another agent's.
	head := append([]string{}, base...)
	head[0] = strings.Replace(rift1, "%s", `"closed"`, 1)
	gitP(t, dir, "checkout", "-q", "-b", "pr")
	storeCommit(t, dir, "close rift one", head...)

	repo := gitutil.Repo{Dir: dir}
	got := DiffPlan(repo, "main", "pr", "", ScopeTouched)

	if len(got.Tracks) != 1 || got.Tracks[0].Name != "p-rift" {
		t.Fatalf("tracks = %+v, want only p-rift", got.Tracks)
	}
	if got.PlansTotal != 2 || len(got.Hidden) != 1 || got.Hidden[0] != "p-flux" {
		t.Errorf("the untouched plan must be counted and named: total=%d hidden=%v", got.PlansTotal, got.Hidden)
	}
	all := strings.Join(append(append(append([]string{got.Current, got.Note},
		got.Completed...), got.Remaining...), got.NewlyCompleted...), " | ")
	if strings.Contains(all, "flux one") || strings.Contains(all, "flux two") {
		t.Errorf("another agent's steps rendered as this PR's position: %s", all)
	}
	if len(got.NewlyCompleted) != 1 || !strings.Contains(got.NewlyCompleted[0], "rift one") {
		t.Errorf("NewlyCompleted = %v, want the one step this PR closed", got.NewlyCompleted)
	}

	// ScopeAll is the explicit opt-back-in to the whole board.
	every := DiffPlan(repo, "main", "pr", "", ScopeAll)
	if len(every.Tracks) != 2 {
		t.Errorf("ScopeAll should render every plan, got %+v", every.Tracks)
	}
	if len(every.NewlyCompleted) != 1 {
		t.Errorf("even under ScopeAll the DELTA is only this PR's: %v", every.NewlyCompleted)
	}
}

// A PR that moves nothing must not fall back to rendering the board.
func TestDiffPlanNoMovementShowsNoTracks(t *testing.T) {
	dir := t.TempDir()
	gitP(t, dir, "init", "-q")
	gitP(t, dir, "checkout", "-q", "-b", "main")
	storeCommit(t, dir, "base",
		`{"id":"p-a-1","title":"a","issue_type":"epic","status":"in_progress"}`,
		`{"id":"p-b-1","title":"b","issue_type":"epic","status":"open"}`)

	gitP(t, dir, "checkout", "-q", "-b", "pr")
	if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitP(t, dir, "add", "-A")
	gitP(t, dir, "commit", "-q", "-m", "code only")

	repo := gitutil.Repo{Dir: dir}
	got := DiffPlan(repo, "main", "pr", "", ScopeTouched)
	if len(got.Tracks) != 0 {
		t.Errorf("no movement must render no plan, got %+v", got.Tracks)
	}
	if !got.Present || got.PlansTotal != 2 {
		t.Errorf("the store is still present and still has 2 plans: %+v", got)
	}
	if !strings.Contains(got.Note, "moves no plan step") {
		t.Errorf("note should say why nothing is shown: %q", got.Note)
	}

	// ...and that is exactly when the code-without-a-plan-step check fires.
	code := model.CodeDelta{Files: []model.FileChange{{Path: "code.go", Status: "A"}}}
	fs := PlanFindings(repo, "pr", "", got, code, false)
	found := false
	for _, f := range fs {
		if strings.Contains(f.Title, "moves no plan step") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the plan-movement finding, got %+v", fs)
	}

	// A store-only / docs-only PR is not work a plan step was tracking.
	docs := model.CodeDelta{Files: []model.FileChange{{Path: "README.md", Status: "M"}}}
	for _, f := range PlanFindings(repo, "pr", "", got, docs, false) {
		if strings.Contains(f.Title, "moves no plan step") {
			t.Error("a docs-only PR must not be nagged for not moving the plan")
		}
	}
}

// No store at all: nugit must stay silent, not nag a repo into adopting bd.
func TestPlanFindingsSilentWithoutAStore(t *testing.T) {
	dir := t.TempDir()
	gitP(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitP(t, dir, "add", "-A")
	gitP(t, dir, "commit", "-q", "-m", "x")
	repo := gitutil.Repo{Dir: dir}
	code := model.CodeDelta{Files: []model.FileChange{{Path: "f.go", Status: "A"}}}
	if fs := PlanFindings(repo, "HEAD", "", model.PlanPosition{}, code, false); len(fs) != 0 {
		t.Errorf("no store must mean no findings, got %+v", fs)
	}
}

// A store defect that predates the check must not turn a repo's gate red on the
// upgrade that introduced it: findings ramp in at warn, and `plan.mode: fail`
// is the deliberate step up.
func TestPlanFindingsRampInAtWarn(t *testing.T) {
	dir := t.TempDir()
	gitP(t, dir, "init", "-q")
	storeCommit(t, dir, "dup",
		`{"id":"p-a-1","title":"a","issue_type":"epic","status":"open"}`,
		`{"id":"p-a-1","title":"a again","issue_type":"epic","status":"closed"}`)
	repo := gitutil.Repo{Dir: dir}
	pos := DiffPlan(repo, "HEAD", "HEAD", "", ScopeTouched)

	warned := PlanFindings(repo, "HEAD", "", pos, model.CodeDelta{}, false)
	if len(warned) == 0 {
		t.Fatal("the duplicate must still be reported")
	}
	for _, f := range warned {
		if f.Severity == model.SevFail {
			t.Errorf("default mode must not gate: %+v", f)
		}
	}
	failed := PlanFindings(repo, "HEAD", "", pos, model.CodeDelta{}, true)
	sawFail := false
	for _, f := range failed {
		if f.Severity == model.SevFail {
			sawFail = true
		}
	}
	if !sawFail {
		t.Errorf("plan.mode: fail must gate on an id collision: %+v", failed)
	}
}
