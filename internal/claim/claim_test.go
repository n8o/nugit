package claim

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/beads"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo makes a git repo with a .nugit/ store, a plan store, one commit on
// main recorded as origin's default branch, and a checked-out task branch.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, ".nugit/config.yml", "{}\n")
	write(t, dir, ".beads/issues.jsonl",
		`{"id":"acme-1","title":"make it fast","status":"in_progress"}`+"\n"+
			`{"id":"acme-2","title":"later","status":"open"}`+"\n"+
			`{"id":"acme-3","title":"old","status":"closed"}`+"\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	git(t, dir, "checkout", "-q", "-b", "task")
	return dir
}

func TestClaimOneObjectivePerBranch(t *testing.T) {
	dir := repo(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s1")
	t.Setenv("AGENTDECK_INSTANCE_ID", "ad1")
	c, err := Add(dir, "acme-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Branch != "task" || c.Session != "s1" || c.AgentSession != "ad1" {
		t.Errorf("claim = %+v", c)
	}
	// A resumed session re-claims the same objective: allowed, and recorded.
	if _, err := Add(dir, "acme-1", false); err != nil {
		t.Errorf("re-claim: %v", err)
	}
	if n := len(Read(dir)); n != 2 {
		t.Errorf("log has %d lines, want 2", n)
	}
	// A different objective on the same branch is new work for a new session.
	if _, err := Add(dir, "acme-2", false); !errors.Is(err, ErrOtherObjective) {
		t.Errorf("second objective: err = %v, want ErrOtherObjective", err)
	}
	if _, err := Add(dir, "acme-2", true); err != nil {
		t.Errorf("forced: %v", err)
	}
	if cur := Resolve(dir); cur.Objective != "acme-2" || cur.Source != FromClaim {
		t.Errorf("resolve = %+v, want acme-2 from the newest claim", cur)
	}
	if _, err := Add(dir, "two ids", false); err == nil {
		t.Error("an id with a space was accepted")
	}
}

// Every worktree of a repo shares one claim log (ADR-0025), so a claim made
// in a linked worktree is visible from the main checkout and vice versa.
func TestClaimsAreSharedAcrossWorktrees(t *testing.T) {
	dir := repo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", "-b", "other", wt)
	if _, err := Add(wt, "acme-2", false); err != nil {
		t.Fatal(err)
	}
	if c, ok := ForBranch(dir, "other"); !ok || c.Objective != "acme-2" {
		t.Errorf("main checkout sees %+v, %v", c, ok)
	}
	if _, err := os.Stat(filepath.Join(wt, ".nugit-local")); err == nil {
		t.Error("the worktree got its own .nugit-local/")
	}
}

func TestDetachedHeadCannotClaim(t *testing.T) {
	dir := repo(t)
	git(t, dir, "checkout", "-q", "--detach")
	if _, err := Add(dir, "acme-1", false); err == nil {
		t.Error("claimed on a detached HEAD")
	}
	if cur := Resolve(dir); cur.Branch != "" {
		t.Errorf("resolve = %+v, want no branch", cur)
	}
}

// With no claim, the branch's own commits name the objective — never commits
// already on the default branch.
func TestResolveFromCommits(t *testing.T) {
	dir := repo(t)
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "old", "-m", "objective: acme-3")
	git(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	git(t, dir, "checkout", "-q", "-b", "fresh")
	if cur := Resolve(dir); cur.Objective != "" {
		t.Fatalf("resolve = %+v, read a commit from the default branch", cur)
	}
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "first", "-m", "objective: acme-1")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "second", "-m", "objective: acme-2")
	if cur := Resolve(dir); cur.Objective != "acme-1" || cur.Source != FromCommits {
		t.Errorf("resolve = %+v, want acme-1 (the branch's first) from commits", cur)
	}
}

func TestStamp(t *testing.T) {
	for _, tc := range []struct {
		name, msg, want string
		added           bool
		other           string
	}{
		{"subject only", "feat: x\n", "feat: x\n\nobjective: acme-1\n", true, ""},
		{"body", "feat: x\n\nWhy it changed.\n", "feat: x\n\nWhy it changed.\n\nobjective: acme-1\n", true, ""},
		{"joins a trailer block", "feat: x\n\nWhy.\n\nlearned: a\nkeywords: b\nCo-Authored-By: C <c@c>\n",
			"feat: x\n\nWhy.\n\nlearned: a\nkeywords: b\nCo-Authored-By: C <c@c>\nobjective: acme-1\n", true, ""},
		{"before git's comments", "feat: x\n\n# Please enter the commit message\n# On branch task\n",
			"feat: x\n\nobjective: acme-1\n\n# Please enter the commit message\n# On branch task\n", true, ""},
		{"already named", "feat: x\n\nobjective: acme-1\n", "feat: x\n\nobjective: acme-1\n", false, ""},
		{"names another", "feat: x\n\nobjective: acme-9\n", "feat: x\n\nobjective: acme-9\n", false, "acme-9"},
		{"a commented-out objective does not count", "feat: x\n\n# objective: acme-9\n",
			"feat: x\n\nobjective: acme-1\n\n# objective: acme-9\n", true, ""},
		{"empty aborts the commit", "\n# comment\n", "\n# comment\n", false, ""},
	} {
		out, added, other := Stamp(tc.msg, "acme-1")
		if out != tc.want || added != tc.added || other != tc.other {
			t.Errorf("%s:\n got %q added=%v other=%q\nwant %q added=%v other=%q",
				tc.name, out, added, other, tc.want, tc.added, tc.other)
		}
	}
}

func TestDigest(t *testing.T) {
	dir := repo(t)
	d := Digest(dir)
	for _, want := range []string{"one session works toward one objective", "No objective is claimed for branch task",
		"nugit objective claim <bead-id>", "- acme-1 — make it fast", "1 other bead(s) are open"} {
		if !strings.Contains(d, want) {
			t.Errorf("unclaimed digest missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "acme-3") {
		t.Errorf("a closed bead was listed:\n%s", d)
	}

	if _, err := Add(dir, "acme-1", false); err != nil {
		t.Fatal(err)
	}
	d = Digest(dir)
	for _, want := range []string{"This session's objective: acme-1 — make it fast (status: in_progress)",
		"commit-msg hook adds `objective: acme-1`", "Stay on this objective"} {
		if !strings.Contains(d, want) {
			t.Errorf("claimed digest missing %q:\n%s", want, d)
		}
	}

	git(t, dir, "checkout", "-q", "main")
	if d := Digest(dir); !strings.Contains(d, "You are on main. Create a branch") {
		t.Errorf("default-branch digest:\n%s", d)
	}
	if len(Digest(dir)) > MaxDigest {
		t.Error("digest over the cap")
	}
}

func TestDigestUnknownBeadAndNoStore(t *testing.T) {
	d := digest(Current{Branch: "task", Objective: "acme-9", Source: FromClaim}, beads.Store{}, "main")
	if !strings.Contains(d, "acme-9. It is not in the plan store") {
		t.Errorf("unknown bead:\n%s", d)
	}
	d = digest(Current{Branch: "task", Objective: "acme-1", Source: FromCommits}, beads.Store{}, "main")
	if !strings.Contains(d, "run `nugit objective claim acme-1`") {
		t.Errorf("commits-only objective should suggest a claim:\n%s", d)
	}
	if d := Digest(t.TempDir()); d != "" {
		t.Errorf("a repo with no .nugit/ got a digest:\n%s", d)
	}
}
