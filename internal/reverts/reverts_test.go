package reverts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/gitutil"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t.Add(12 * time.Hour)
}

func ch(sha, when, subject string, churn int, files ...string) gitutil.FirstParentChange {
	return gitutil.FirstParentChange{SHA: sha, Time: day(when), Subject: subject, Files: files, Churn: churn}
}

func TestBuildMonthsRevertsAndControls(t *testing.T) {
	changes := []gitutil.FirstParentChange{
		ch("a1", "2026-03-02", "feat: big", 100, "a.go", "b.go"),
		ch("b1", "2026-03-03", "feat: near in size", 90, "c.go"),
		ch("b2", "2026-03-04", "feat: as near, closer in files", 110, "d.go", "e.go"),
		ch("c1", "2026-03-05", "feat: nearest but fixed later", 101, "f.go"),
		ch("c2", "2026-03-20", "fix(f): the follow-up", 3, "f.go"),
		ch("r1", "2026-03-25", `Revert "feat: big"`, 100, "a.go", "b.go"),
		ch("r2", "2026-04-02", `Revert "Revert "feat: big""`, 100, "a.go", "b.go"),
		ch("x1", "2026-04-03", "feat: reverted inside its own change", 10, "x.go"),
		ch("old", "2026-01-15", "feat: before the window", 5, "z.go"),
		ch("tip", "2026-05-20", "chore: tip", 1, "t.go"),
	}
	changes[5].Body = "This reverts commit a1."
	edges := []Edge{
		{Commit: "r1", Landed: "r1", Reverted: "a1"},
		{Commit: "r2", Landed: "r2", Reverted: "r1", Reland: true}, // a re-land is not a revert
		{Commit: "x1r", Landed: "x1", Reverted: "x1"},              // reverted before it merged
		{Commit: "o1", Landed: "tip", Reverted: "old"},             // the change predates the window
		{Commit: "u1", Landed: "", Reverted: "b1"},                 // the revert could not be placed
	}
	rep := Build(changes, edges, day("2026-05-20"), 3)

	if got := len(rep.Months); got != 3 || rep.Months[0].Month != "2026-03" || rep.Months[2].Month != "2026-05" {
		t.Fatalf("months = %+v", rep.Months)
	}
	mar := rep.Months[0]
	if mar.Merged != 6 || mar.Reverted != 1 || mar.RevertedMedianChurn != 100 {
		t.Errorf("March = %+v", mar)
	}
	if want := 1.0 / 6; mar.Rate < want-1e-9 || mar.Rate > want+1e-9 {
		t.Errorf("March rate = %v, want %v", mar.Rate, want)
	}
	if len(rep.Reverts) != 1 || rep.Reverts[0].Reverted.SHA != "a1" || rep.Reverts[0].AgeDays != 23 {
		t.Fatalf("reverts = %+v", rep.Reverts)
	}
	// c1 is nearest in churn but a fix touched its file within 30 days; b1
	// and b2 are equally near (10 lines), and b2 is nearer in files.
	if len(rep.Controls) != 1 || rep.Controls[0].Change == nil || rep.Controls[0].Change.SHA != "b2" {
		t.Fatalf("controls = %+v", rep.Controls)
	}
	md := rep.Markdown()
	for _, want := range []string{"| 2026-03 | 6 | 1 | 16.7% |", "reverted `a1` feat: big", "for `a1`: `b2`"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}

func TestControlsAreYoungUnusedAndMayBeMissing(t *testing.T) {
	changes := []gitutil.FirstParentChange{
		ch("a1", "2026-05-01", "feat: one", 10, "a.go"),
		ch("a2", "2026-05-02", "feat: two", 10, "b.go"),
		ch("c1", "2026-05-03", "feat: the only clean one", 12, "c.go"),
		ch("y1", "2026-05-18", "feat: too young to be clean", 10, "y.go"),
	}
	edges := []Edge{{Commit: "r", Landed: "c1", Reverted: "a1"}, {Commit: "s", Landed: "c1", Reverted: "a2"}}
	// a1 and a2 were reverted, c1 landed the reverts, and y1 is under 30 days
	// old at the tip. Nothing is left to match either one.
	rep := Build(changes, edges, day("2026-05-20"), 1)
	if len(rep.Controls) != 2 {
		t.Fatalf("controls = %+v", rep.Controls)
	}
	for _, c := range rep.Controls {
		if c.Change != nil {
			t.Errorf("control for %s = %+v, want none", c.For, c.Change)
		}
	}
	if !strings.Contains(rep.Markdown(), "no clean change left in its month") {
		t.Error("a missing control is not said")
	}
}

func TestMergeTitleMarksAFix(t *testing.T) {
	m := ch("m1", "2026-05-01", "Merge pull request #7 from o/fix-thing", 4, "a.go")
	m.Body = "fix(a): the thing\n\nmore"
	if got := title(m); got != "fix(a): the thing" || !fixRE.MatchString(got) {
		t.Errorf("title = %q", got)
	}
	if fixRE.MatchString("fixture: not a fix") || !fixRE.MatchString("fix!: breaking") {
		t.Error("fix pattern")
	}
}

// --- against real git ---

func git(t *testing.T, dir string, at time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	d := at.UTC().Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+d, "GIT_AUTHOR_DATE="+d)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, rel string, lines int) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("line\n", lines)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, msg string, at time.Time) string {
	git(t, dir, at, "add", "-A")
	git(t, dir, at, "commit", "-q", "-m", msg)
	return git(t, dir, at, "rev-parse", "HEAD")
}

func newRepo(t *testing.T) string {
	dir := t.TempDir()
	git(t, dir, day("2026-02-20"), "init", "-q")
	write(t, dir, "README", 1)
	commit(t, dir, "init", day("2026-02-20"))
	return dir
}

// mergeBranch commits files on a new branch and merges it --no-ff, the way a
// merge-commit repo lands a PR. It returns the branch's first commit and the
// merge.
func mergeBranch(t *testing.T, dir, name, title string, at time.Time, files map[string]int) (inner, merge string) {
	git(t, dir, at, "checkout", "-q", "-b", name)
	for f, n := range files {
		write(t, dir, f, n)
	}
	inner = commit(t, dir, title, at)
	git(t, dir, at, "checkout", "-q", "main")
	git(t, dir, at.Add(time.Hour), "merge", "-q", "--no-ff", "-m", "Merge pull request from o/"+name+"\n\n"+title, name)
	return inner, git(t, dir, at, "rev-parse", "HEAD")
}

func TestMergeCommitHistory(t *testing.T) {
	dir := newRepo(t)
	a1, m1 := mergeBranch(t, dir, "feat-a", "feat: a", day("2026-03-02"), map[string]int{"a.go": 10, "b.go": 5})
	_, m2 := mergeBranch(t, dir, "feat-b", "feat: b", day("2026-03-05"), map[string]int{"c.go": 12})
	mergeBranch(t, dir, "feat-c", "feat: c", day("2026-03-07"), map[string]int{"d.go": 14})
	write(t, dir, "d.go", 15)
	commit(t, dir, "fix: d", day("2026-03-10")) // disqualifies the nearest-size change
	// The revert of the merged branch's inner commit arrives on its own branch.
	git(t, dir, day("2026-03-20"), "checkout", "-q", "-b", "revert-a")
	git(t, dir, day("2026-03-20"), "revert", "--no-edit", a1)
	r1 := git(t, dir, day("2026-03-20"), "rev-parse", "HEAD")
	git(t, dir, day("2026-03-21"), "checkout", "-q", "main")
	git(t, dir, day("2026-03-21"), "merge", "-q", "--no-ff", "-m", "Merge pull request from o/revert-a\n\nRevert a", "revert-a")
	m4 := git(t, dir, day("2026-03-21"), "rev-parse", "HEAD")
	git(t, dir, day("2026-04-01"), "revert", "--no-edit", r1) // the re-land
	write(t, dir, "t.go", 1)
	tip := commit(t, dir, "chore: tip", day("2026-05-10"))

	rep, err := Run(Options{RepoDir: dir, Ref: "main", Months: 3})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tip != tip || rep.Months[0].Month != "2026-03" || rep.Months[0].Merged != 5 || rep.Months[1].Merged != 1 {
		t.Fatalf("report = %+v", rep.Months)
	}
	if len(rep.Reverts) != 1 {
		t.Fatalf("reverts = %+v (the re-land must not count)", rep.Reverts)
	}
	r := rep.Reverts[0]
	if r.Reverted.SHA != m1 || r.Landed.SHA != m4 || r.Commit != r1 || r.Reverted.Churn != 15 || r.Reverted.Files != 2 || r.AgeDays != 18 {
		t.Errorf("revert = %+v", r)
	}
	if c := rep.Controls; len(c) != 1 || c[0].Change == nil || c[0].Change.SHA != m2 {
		t.Errorf("controls = %+v, want %s (the fixed change is nearer but not clean)", c, m2)
	}
}

func TestSquashHistory(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "x.go", 20)
	s1 := commit(t, dir, "feat: x", day("2026-03-02"))
	write(t, dir, "y.go", 18)
	s2 := commit(t, dir, "feat: y", day("2026-03-03"))
	write(t, dir, "z.go", 30)
	commit(t, dir, "feat: z", day("2026-03-04"))
	git(t, dir, day("2026-03-05"), "revert", "--no-edit", s1)
	r := git(t, dir, day("2026-03-05"), "rev-parse", "HEAD")
	write(t, dir, "t.go", 1)
	commit(t, dir, "chore: tip", day("2026-05-10"))

	rep, err := Run(Options{RepoDir: dir, Ref: "main", Months: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Reverts) != 1 || rep.Reverts[0].Reverted.SHA != s1 || rep.Reverts[0].Landed.SHA != r || rep.Reverts[0].AgeDays != 3 {
		t.Fatalf("reverts = %+v", rep.Reverts)
	}
	if c := rep.Controls; len(c) != 1 || c[0].Change == nil || c[0].Change.SHA != s2 {
		t.Errorf("controls = %+v, want %s", c, s2)
	}
	// "now" is the tip, not the clock: the same ref gives the same report.
	again, _ := Run(Options{RepoDir: dir, Ref: "main", Months: 3})
	if again.Markdown() != rep.Markdown() {
		t.Error("report is not deterministic")
	}
}

func TestNoRefNeedsOriginDefault(t *testing.T) {
	dir := newRepo(t)
	if _, err := Run(Options{RepoDir: dir}); err == nil || !strings.Contains(err.Error(), "origin default branch") {
		t.Errorf("err = %v", err)
	}
}
