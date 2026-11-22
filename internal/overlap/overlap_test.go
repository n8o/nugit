package overlap

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
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

// commit writes files and commits them on whatever is checked out.
func commit(t *testing.T, dir, msg string, files map[string]string) string {
	t.Helper()
	for p, c := range files {
		write(t, dir, p, c)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

// branch checks out a new branch at from.
func branch(t *testing.T, dir, name, from string) {
	t.Helper()
	git(t, dir, "checkout", "-q", "-b", name, from)
}

func decision(id string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: decision\nscope: global\nstatus: proposed\n" +
		"created: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# " + id + "\n\nbody\n"
}

const tenLines = "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"

var testModel = model.Model{Components: []model.Component{
	{ID: "render", Name: "Renderer", Paths: []string{"internal/render/**"}},
	{ID: "engine", Name: "Engine", Paths: []string{"internal/engine/**"}},
}}

// seed makes a repo whose main holds a ten-line file and two components.
func seed(t *testing.T) (dir, main string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	main = commit(t, dir, "init", map[string]string{
		"a.txt":                  tenLines,
		"internal/render/r.go":   "package render\n",
		"internal/engine/e.go":   "package engine\n",
		".nugit/decisions/.keep": "",
	})
	return dir, main
}

func work(ref string, pr int) model.OpenWork {
	return model.OpenWork{PR: pr, Branch: ref, Ref: ref}
}

func compute(t *testing.T, dir, head string, others ...model.OpenWork) model.OverlapView {
	t.Helper()
	v, err := Compute(Input{Repo: gitutil.Repo{Dir: dir}, Base: "main", Head: head, Model: testModel, Others: others})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func only(t *testing.T, v model.OverlapView) model.Overlap {
	t.Helper()
	if len(v.Items) != 1 {
		t.Fatalf("items = %+v, want exactly one", v.Items)
	}
	return v.Items[0].Overlap
}

// Two changes to the same lines of one file: the file is shared, and the
// merge simulation says it conflicts.
func TestASharedFileThatConflictsIsNamed(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "mine", main)
	commit(t, dir, "mine", map[string]string{"a.txt": "ONE\n" + tenLines[2:]})
	branch(t, dir, "theirs", main)
	commit(t, dir, "theirs", map[string]string{"a.txt": "uno\n" + tenLines[2:]})

	o := only(t, compute(t, dir, "mine", work("theirs", 7)))
	if !reflect.DeepEqual(o.Files, []string{"a.txt"}) || !o.MergeChecked || !reflect.DeepEqual(o.Conflicts, []string{"a.txt"}) {
		t.Fatalf("overlap = %+v, want a.txt shared and conflicting", o)
	}
}

// Two changes to different ends of one file share it and merge cleanly.
func TestASharedFileThatMergesCleanlyIsNotAConflict(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "mine", main)
	commit(t, dir, "mine", map[string]string{"a.txt": "ONE\n" + tenLines[2:]})
	branch(t, dir, "theirs", main)
	commit(t, dir, "theirs", map[string]string{"a.txt": tenLines[:len(tenLines)-3] + "TEN\n"})

	o := only(t, compute(t, dir, "mine", work("theirs", 7)))
	if !reflect.DeepEqual(o.Files, []string{"a.txt"}) || !o.MergeChecked || len(o.Conflicts) != 0 {
		t.Fatalf("overlap = %+v, want a.txt shared, merge checked, no conflict", o)
	}
}

// Different files in one component share the component, by its name.
func TestDifferentFilesInOneComponentShareIt(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "mine", main)
	commit(t, dir, "mine", map[string]string{"internal/render/a.go": "package render\n"})
	branch(t, dir, "theirs", main)
	commit(t, dir, "theirs", map[string]string{"internal/render/b.go": "package render\n"})

	o := only(t, compute(t, dir, "mine", work("theirs", 7)))
	if len(o.Files) != 0 || !reflect.DeepEqual(o.Components, []string{"Renderer"}) {
		t.Fatalf("overlap = %+v, want only the Renderer component", o)
	}
}

// Both branches mint ADR-0007: each passes alone, and main breaks once both
// merge. That is the collision the view leads with.
func TestBothAddingOneRecordIdIsACollision(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "mine", main)
	commit(t, dir, "mine", map[string]string{".nugit/decisions/0007-mine.md": decision("ADR-0007")})
	branch(t, dir, "theirs", main)
	commit(t, dir, "theirs", map[string]string{".nugit/decisions/0007-theirs.md": decision("ADR-0007")})

	o := only(t, compute(t, dir, "mine", work("theirs", 7)))
	want := []string{"both add ADR-0007: `.nugit/decisions/0007-mine.md` here, `.nugit/decisions/0007-theirs.md` there"}
	if !reflect.DeepEqual(o.Collisions, want) {
		t.Fatalf("collisions = %q, want %q", o.Collisions, want)
	}
}

// One decision number under two ids is the duplicate-decision-number key.
func TestBothTakingOneDecisionNumberIsACollision(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "mine", main)
	commit(t, dir, "mine", map[string]string{".nugit/decisions/0007-mine.md": decision("ADR-0007")})
	branch(t, dir, "theirs", main)
	commit(t, dir, "theirs", map[string]string{".nugit/decisions/0007-theirs.md": decision("ADR-0070")})

	o := only(t, compute(t, dir, "mine", work("theirs", 7)))
	want := []string{"both take decision number 0007 in `.nugit/decisions`: ADR-0007 here, ADR-0070 there"}
	if !reflect.DeepEqual(o.Collisions, want) {
		t.Fatalf("collisions = %q, want %q", o.Collisions, want)
	}
}

// A branch stacked on this one, and an older push of it, are this change's
// own line: they are not compared at all.
func TestWorkOnTheSameLineIsLeftOut(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "older", main)
	commit(t, dir, "older", map[string]string{"a.txt": "ONE\n" + tenLines[2:]})
	branch(t, dir, "mine", "older")
	commit(t, dir, "mine", map[string]string{"a.txt": "ONE\nTWO\n" + tenLines[4:]})
	branch(t, dir, "stacked", "mine")
	commit(t, dir, "stacked", map[string]string{"a.txt": "ONE\nTWO\nTHREE\n" + tenLines[6:]})

	v := compute(t, dir, "mine", work("older", 1), work("stacked", 2))
	if v.Checked != 0 || len(v.Items) != 0 {
		t.Fatalf("view = %+v, want nothing checked", v)
	}
}

// Unrelated work is counted as checked and not listed.
func TestUnrelatedWorkIsCheckedNotListed(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "mine", main)
	commit(t, dir, "mine", map[string]string{"internal/render/a.go": "package render\n"})
	branch(t, dir, "theirs", main)
	commit(t, dir, "theirs", map[string]string{"internal/engine/b.go": "package engine\n"})

	v := compute(t, dir, "mine", work("theirs", 7))
	if v.Checked != 1 || len(v.Items) != 0 {
		t.Fatalf("view = %+v, want 1 checked, none listed", v)
	}
}

// A change is what lies past its merge base. Work cut before main moved does
// not own what main gained since, so main's later edit to a file this branch
// also edits is not the other branch's overlap.
func TestAChangeIsMeasuredFromItsMergeBase(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "theirs", main)
	commit(t, dir, "theirs", map[string]string{"internal/engine/b.go": "package engine\n"})
	git(t, dir, "checkout", "-q", "main")
	commit(t, dir, "main moves", map[string]string{"internal/render/r.go": "package render // moved\n"})
	branch(t, dir, "mine", "main")
	commit(t, dir, "mine", map[string]string{"internal/render/r.go": "package render // mine\n"})

	v := compute(t, dir, "mine", work("theirs", 7))
	if len(v.Items) != 0 {
		t.Fatalf("items = %+v, want none: main's edit is not theirs", v.Items)
	}
}

// A collision outranks a conflict, which outranks shared files, which
// outrank a shared component.
func TestItemsAreOrderedByWhatTheyBreak(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "mine", main)
	commit(t, dir, "mine", map[string]string{
		"a.txt":                         "ONE\n" + tenLines[2:],
		"internal/render/a.go":          "package render\n",
		".nugit/decisions/0007-mine.md": decision("ADR-0007"),
	})
	branch(t, dir, "component", main)
	commit(t, dir, "c", map[string]string{"internal/render/z.go": "package render\n"})
	branch(t, dir, "conflict", main)
	commit(t, dir, "c", map[string]string{"a.txt": "uno\n" + tenLines[2:]})
	branch(t, dir, "collide", main)
	commit(t, dir, "c", map[string]string{".nugit/decisions/0007-x.md": decision("ADR-0007")})

	v := compute(t, dir, "mine", work("component", 1), work("conflict", 2), work("collide", 3))
	var got []string
	for _, it := range v.Items {
		got = append(got, it.With.Branch)
	}
	if want := []string{"collide", "conflict", "component"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// -pairs compares every two pieces of open work, without a head.
func TestPairsCompareTheOpenWorkWithEachOther(t *testing.T) {
	dir, main := seed(t)
	branch(t, dir, "a", main)
	commit(t, dir, "a", map[string]string{"a.txt": "ONE\n" + tenLines[2:]})
	branch(t, dir, "b", main)
	commit(t, dir, "b", map[string]string{"a.txt": "uno\n" + tenLines[2:]})
	branch(t, dir, "c", main)
	commit(t, dir, "c", map[string]string{"internal/engine/c.go": "package engine\n"})

	v, err := Compute(Input{Repo: gitutil.Repo{Dir: dir}, Base: "main", Model: testModel,
		Others: []model.OpenWork{work("a", 1), work("b", 2), work("c", 3)}, Pairs: true})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Pairwise || v.Checked != 3 || len(v.Pairs) != 1 || v.Pairs[0].A.PR != 1 || v.Pairs[0].B.PR != 2 ||
		!reflect.DeepEqual(v.Pairs[0].Conflicts, []string{"a.txt"}) {
		t.Fatalf("view = %+v, want the one pair #1/#2 conflicting in a.txt", v)
	}
}

// Gather reads branches already fetched, and no more: an old push, one
// already squash-merged, the target, and the reviewed branch are not open
// work.
func TestGatherReadsRecentUnmergedBranches(t *testing.T) {
	dir, main := seed(t)
	git(t, dir, "update-ref", "refs/remotes/origin/main", main)
	if _, err := Gather(dir, GatherOptions{NoGitHub: true, Recent: DefaultRecent}); err != ErrNoBase {
		t.Fatalf("err = %v, want ErrNoBase without origin/HEAD", err)
	}
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	now := time.Now()

	branch(t, dir, "fresh", main)
	git(t, dir, "update-ref", "refs/remotes/origin/fresh", commit(t, dir, "f", map[string]string{"f.txt": "f\n"}))
	branch(t, dir, "mine", main)
	git(t, dir, "update-ref", "refs/remotes/origin/mine", commit(t, dir, "m", map[string]string{"m.txt": "m\n"}))
	branch(t, dir, "old", main)
	write(t, dir, "o.txt", "o\n")
	git(t, dir, "add", "-A")
	old := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	cmd := exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "o")
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+old, "GIT_AUTHOR_DATE="+old)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	git(t, dir, "update-ref", "refs/remotes/origin/old", git(t, dir, "rev-parse", "HEAD"))
	// squashed: its content is on main under another commit.
	branch(t, dir, "squashed", main)
	sq := commit(t, dir, "s", map[string]string{"s.txt": "s\n"})
	git(t, dir, "update-ref", "refs/remotes/origin/squashed", sq)
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "update-ref", "refs/remotes/origin/main", commit(t, dir, "squash s", map[string]string{"s.txt": "s\n"}))

	g, err := Gather(dir, GatherOptions{NoGitHub: true, Recent: DefaultRecent, Exclude: []string{"mine"}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range g.Works {
		got = append(got, w.Branch)
	}
	if !reflect.DeepEqual(got, []string{"origin/fresh"}) || g.Base != "origin/main" {
		t.Fatalf("works = %v (base %s), want only origin/fresh", got, g.Base)
	}
	if g.Works[0].Pushed == "" || !strings.Contains(g.Source, "no pull requests") {
		t.Fatalf("work = %+v, source %q", g.Works[0], g.Source)
	}
}

// With gh: an open PR is read at its head commit and measured against its
// own base; a fork's PR whose head is not here is named, not fetched; its
// branch is not listed a second time, and the branch a PR targets is never
// open work.
func TestGatherReadsOpenPullRequests(t *testing.T) {
	dir, main := seed(t)
	git(t, dir, "update-ref", "refs/remotes/origin/main", main)
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	branch(t, dir, "feat", main)
	tip := commit(t, dir, "f", map[string]string{"f.txt": "f\n"})
	git(t, dir, "update-ref", "refs/remotes/origin/feat", tip)
	branch(t, dir, "release", main)
	git(t, dir, "update-ref", "refs/remotes/origin/release", commit(t, dir, "r", map[string]string{"r.txt": "r\n"}))

	branch(t, dir, "abandoned", main)
	dead := commit(t, dir, "a", map[string]string{"x.txt": "x\n"})
	git(t, dir, "update-ref", "refs/remotes/origin/abandoned", dead)
	branch(t, dir, "reopened", main)
	commit(t, dir, "a", map[string]string{"y.txt": "y\n"})
	git(t, dir, "update-ref", "refs/remotes/origin/reopened", commit(t, dir, "more", map[string]string{"y.txt": "yy\n"}))
	branch(t, dir, "rewritten", main)
	git(t, dir, "update-ref", "refs/remotes/origin/rewritten", commit(t, dir, "w", map[string]string{"w.txt": "w\n"}))
	listFinished = func(string) finishedPRs {
		// abandoned's tip is a closed PR's head; rewritten's PR ended after
		// its tip was committed (the head moved, the branch was left);
		// reopened was committed to after its PR ended.
		return finishedPRs{
			tips: map[string]bool{dead: true, git(t, dir, "rev-parse", "reopened~1"): true},
			ended: map[string]time.Time{
				"rewritten": time.Now().Add(time.Hour),
				"reopened":  time.Now().Add(-time.Hour),
			},
		}
	}
	listPRs = func(string) ([]pullRequest, bool) {
		return []pullRequest{
			{Number: 5, Title: "Feat", Head: "feat", Oid: tip, BaseRef: "release", Draft: true},
			{Number: 6, Head: "someone/fix", Oid: strings.Repeat("ab", 20), BaseRef: "main", Fork: true},
		}, true
	}
	t.Cleanup(func() { listPRs, listFinished = openPRs, finishedTips })

	g, err := Gather(dir, GatherOptions{Recent: DefaultRecent})
	if err != nil {
		t.Fatal(err)
	}
	want := []model.OpenWork{
		{PR: 5, Title: "Feat", Draft: true, Branch: "feat", Ref: tip, Base: "origin/release"},
		{Branch: "origin/reopened", Pushed: g.Works[len(g.Works)-1].Pushed, Ref: "origin/reopened"},
	}
	if !reflect.DeepEqual(g.Works, want) || !reflect.DeepEqual(g.Unread, []string{"#6"}) {
		t.Fatalf("works = %+v unread = %v, want %+v and [#6]", g.Works, g.Unread, want)
	}
	if !strings.HasPrefix(g.Source, "open pull requests (gh)") {
		t.Fatalf("source = %q", g.Source)
	}
}

func TestParseFinishedKeepsEachBranchsLatestEnd(t *testing.T) {
	f := parseFinished([]byte(`[{"headRefName":"a","headRefOid":"x1","closedAt":"2026-11-01T00:00:00Z"},` +
		`{"headRefName":"a","headRefOid":"x2","closedAt":"2026-11-03T00:00:00Z"},{"headRefName":"b","headRefOid":""}]`))
	if !f.tips["x1"] || !f.tips["x2"] || len(f.tips) != 2 || f.ended["a"].Day() != 3 || !f.ended["b"].IsZero() {
		t.Fatalf("finished = %+v", f)
	}
}

func TestParsePRsDropsRowsWithoutAHead(t *testing.T) {
	prs, ok := parsePRs([]byte(`[{"number":1,"headRefName":"a"},{"number":2,"headRefName":""},{"number":0,"headRefName":"b"}]`))
	if !ok || len(prs) != 1 || prs[0].Number != 1 {
		t.Fatalf("prs = %+v ok=%v", prs, ok)
	}
	if _, ok := parsePRs([]byte("not json")); ok {
		t.Fatal("bad JSON parsed")
	}
}
