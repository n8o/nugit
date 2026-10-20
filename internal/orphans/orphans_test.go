package orphans

import (
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/claim"
	"github.com/n8o/nugit/internal/localmem"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func ago(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }

func store(lines ...string) beads.Store {
	return beads.Store{Issues: beads.ParseJSONL([]byte(strings.Join(lines, "\n")))}
}

func base() Input {
	return Input{Now: now, Stale: 7 * 24 * time.Hour, Default: "origin/main", Named: map[string]time.Time{}}
}

func kinds(r Report) map[Kind][]Orphan {
	m := map[Kind][]Orphan{}
	for _, o := range r.Orphans {
		m[o.Kind] = append(m[o.Kind], o)
	}
	return m
}

func TestStalledObjective(t *testing.T) {
	in := base()
	in.Store = store(
		`{"id":"a-1","title":"quiet","status":"in_progress","updated_at":"2026-09-01T00:00:00Z"}`,
		`{"id":"a-2","title":"recent","status":"in_progress","updated_at":"2026-10-18T00:00:00Z"}`,
		`{"id":"a-3","title":"backlog","status":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		`{"id":"a-4","title":"worked then left","status":"open"}`,
		`{"id":"a-5","title":"done","status":"closed","updated_at":"2026-01-01T00:00:00Z"}`,
		`{"id":"a-6","title":"held by a live session","status":"in_progress","updated_at":"2026-09-01T00:00:00Z"}`,
	)
	in.Named["a-4"] = ago(30)
	in.Claims = []claim.Claim{{Branch: "b6", Objective: "a-6", Time: ago(30).Format(time.RFC3339), AgentSession: "s6"}}
	in.Sessions = map[string]Session{"s6": {ID: "s6", Status: "running"}}

	got := kinds(Compute(in))[StalledObjective]
	var ids []string
	for _, o := range got {
		ids = append(ids, o.Subject)
	}
	// Oldest first; never-started backlog, recent work, done beads and a bead a
	// live session holds are not orphans.
	if strings.Join(ids, ",") != "a-1,a-4" {
		t.Fatalf("stalled = %v, want a-1,a-4", ids)
	}
	if !strings.Contains(got[1].Detail, "earlier commits name it") {
		t.Errorf("detail = %q", got[1].Detail)
	}
}

func TestStoppedSession(t *testing.T) {
	in := base()
	in.Store = store(`{"id":"a-1","title":"open one","status":"in_progress"}`, `{"id":"a-2","title":"done","status":"closed"}`)
	in.Claims = []claim.Claim{
		{Branch: "x", Objective: "a-1", Time: ago(3).Format(time.RFC3339), AgentSession: "s1"},
		{Branch: "y", Objective: "a-2", Time: ago(3).Format(time.RFC3339), AgentSession: "s2"},
		{Branch: "z", Objective: "a-1", Time: ago(3).Format(time.RFC3339), AgentSession: "gone"},
	}
	in.Sessions = map[string]Session{
		"s1": {ID: "s1", Title: "fix it", Status: "stopped", LastActivity: ago(2)},
		"s2": {ID: "s2", Status: "stopped"},
	}
	got := kinds(Compute(in))[StoppedSession]
	if len(got) != 2 {
		t.Fatalf("stopped = %+v, want x and z (a-2 is done)", got)
	}
	if !strings.Contains(got[0].Detail+got[1].Detail, `"fix it" (s1) claimed it on x and is stopped`) ||
		!strings.Contains(got[0].Action+got[1].Action, "agent-deck session start s1") {
		t.Errorf("stopped = %+v", got)
	}
	if !strings.Contains(got[0].Detail+got[1].Detail, "no longer exists") {
		t.Errorf("a vanished session is not reported: %+v", got)
	}

	// Without agent-deck there is nothing to say about sessions.
	in.Sessions = nil
	if got := kinds(Compute(in))[StoppedSession]; len(got) != 0 {
		t.Errorf("no sessions consulted, got %+v", got)
	}
}

func TestBranches(t *testing.T) {
	in := base()
	in.GitHub = true
	in.Branches = []Branch{
		{Name: "fresh", Tip: ago(1), Ahead: 2},                                          // recent: not stray
		{Name: "old", Tip: ago(20), Ahead: 3, Worktree: "/w/old"},                       // stray, no PR
		{Name: "squashed", Tip: ago(20), Ahead: 3, PR: &PR{Number: 7, State: "MERGED"}}, // merged by PR
		{Name: "same-tree", Tip: ago(20), Ahead: 1, ContentMerged: true},                // merged by content
		{Name: "rejected", Tip: ago(20), Ahead: 1, PR: &PR{Number: 9, State: "CLOSED"}},
		{Name: "waiting", Tip: ago(20), Ahead: 1, PR: &PR{Number: 11, State: "OPEN"}, Objective: "a-1"},
		{Name: "merged-recently", Tip: ago(1), Ahead: 0},
	}
	k := kinds(Compute(in))
	var stray, merged []string
	for _, o := range k[StrayBranch] {
		stray = append(stray, o.Subject)
	}
	for _, o := range k[MergedBranch] {
		merged = append(merged, o.Subject)
	}
	if strings.Join(stray, ",") != "old,rejected,waiting" {
		t.Errorf("stray = %v", stray)
	}
	if strings.Join(merged, ",") != "same-tree,squashed" {
		t.Errorf("merged = %v", merged)
	}
	for _, o := range k[StrayBranch] {
		switch o.Subject {
		case "old":
			if !strings.Contains(o.Detail, "(worktree /w/old)") || !strings.Contains(o.Detail, "; no PR") ||
				!strings.Contains(o.Action, "remove its worktree") {
				t.Errorf("old = %+v", o)
			}
		case "rejected":
			if !strings.Contains(o.Detail, "PR #9 was closed without merging") {
				t.Errorf("rejected = %+v", o)
			}
		case "waiting":
			if !strings.Contains(o.Detail, "objective a-1; PR #11 is open") || !strings.Contains(o.Action, "finish PR #11") {
				t.Errorf("waiting = %+v", o)
			}
		}
	}
}

func TestClosedWithWorkLeft(t *testing.T) {
	in := base()
	in.Store = store(`{"id":"a-1","title":"closed early","status":"closed"}`)
	in.Branches = []Branch{{Name: "rest", Tip: ago(2), Ahead: 2, Objective: "a-1"}}
	got := kinds(Compute(in))[ClosedUnmerged]
	if len(got) != 1 || !strings.Contains(got[0].Detail, "rest still has 2 commits") {
		t.Fatalf("closed-with-work-left = %+v", got)
	}
	// Once that work is merged, the closed bead is simply done.
	in.Branches[0].PR = &PR{Number: 3, State: "MERGED"}
	if got := kinds(Compute(in))[ClosedUnmerged]; len(got) != 0 {
		t.Errorf("merged work still reported: %+v", got)
	}
}

func TestStaleNotes(t *testing.T) {
	in := base()
	in.Notes = []localmem.Entry{
		{Time: ago(1).Format(time.RFC3339), Kind: "note", Text: "fresh"},
		{Time: ago(10).Format(time.RFC3339), Kind: "lesson", Text: "the cache\nlies"},
	}
	got := kinds(Compute(in))[StaleNote]
	if len(got) != 1 || got[0].Detail != "lesson: the cache lies" {
		t.Fatalf("notes = %+v", got)
	}
}

func TestMarkdown(t *testing.T) {
	in := base()
	in.Store = store(`{"id":"a-1","title":"quiet","status":"in_progress","updated_at":"2026-09-01T00:00:00Z"}`)
	for i := 0; i < maxNamed+2; i++ {
		in.Branches = append(in.Branches, Branch{Name: "m" + string(rune('a'+i)), Tip: ago(30)})
	}
	md := Markdown(Compute(in))
	for _, want := range []string{
		"## nugit orphans — 1 to decide",
		"agent-deck was not consulted", "GitHub was not consulted",
		"### Stalled objectives (1)", "- **a-1** — quiet · idle 49 days", "➜ resume it",
		"### Merged branches left behind (12)", "+2 more",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Index(md, "Stalled") > strings.Index(md, "Merged branches") {
		t.Error("cleanup is listed before decisions")
	}
	if md := Markdown(Compute(base())); !strings.Contains(md, "Nothing is orphaned.") {
		t.Errorf("empty report:\n%s", md)
	}
}

func TestParseExternal(t *testing.T) {
	prs := parsePRs([]byte(`[{"number":9,"state":"OPEN","headRefName":"x"},{"number":3,"state":"MERGED","headRefName":"x"},{"number":2,"state":"CLOSED","headRefName":"y"}]`))
	if prs["x"].Number != 9 || prs["y"].State != "CLOSED" {
		t.Errorf("prs = %+v (newest PR per head should win)", prs)
	}
	if parsePRs([]byte("not json")) != nil {
		t.Error("bad gh output parsed")
	}
	ss := parseSessions([]byte(`[{"id":"s1","title":"t","status":"waiting","last_activity_at":"2026-10-19T10:00:00-04:00"}]`))
	if s := ss["s1"]; !s.Live() || s.LastActivity.IsZero() {
		t.Errorf("session = %+v", s)
	}
	if (Session{Status: "error"}).Live() || (Session{Status: "stopped"}).Live() {
		t.Error("a dead session counted as live")
	}
}
