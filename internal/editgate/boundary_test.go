package editgate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/usage"
)

func sessionStart(t *testing.T, dir, source, session string, at time.Time, env map[string]string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"session_id": session, "cwd": dir, "hook_event_name": "SessionStart", "source": source,
	})
	o := opts(env)
	o.Now = at
	SessionStart(b, o)
}

func boundaries(t *testing.T, dir string) []usage.Record {
	t.Helper()
	rs, err := usage.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []usage.Record
	for _, r := range rs {
		if r.Source == usage.SourceBoundary {
			out = append(out, r)
		}
	}
	return out
}

// A compaction ends the cover of every call made before it (ADR-0054): the
// edit is uncovered, recorded lost_to:compact, and the agent is told why the
// call it remembers stopped counting. A call after the compaction covers.
func TestCompactionEndsSessionCover(t *testing.T) {
	dir := repo(t, "")
	file := filepath.Join(dir, "internal/foo/foo.go")
	logCall(t, dir, usage.Record{Path: "internal/foo", Component: "foo", Session: "s1", Time: t0.Add(-10 * time.Minute)})
	sessionStart(t, dir, "compact", "s1", t0.Add(-5*time.Minute), nil)

	o := run(t, dir, input(t, dir, "Edit", file, "s1"), nil)
	if o == nil {
		t.Fatal("an edit after a compaction, with only a call from before it, must be uncovered")
	}
	if msg := o.HookSpecificOutput.AdditionalContext; !strings.Contains(msg, "compacted") {
		t.Errorf("the message must say the earlier call stopped counting: %q", msg)
	}
	r := hookRecords(t, dir)[0]
	if r.Covered == nil || *r.Covered || r.LostTo != "compact" {
		t.Errorf("want uncovered, lost_to compact: %+v", r)
	}

	logCall(t, dir, usage.Record{Path: "internal/foo", Component: "foo", Session: "s1", Time: t0.Add(-time.Minute)})
	if o := run(t, dir, input(t, dir, "Edit", file, "s1"), nil); o != nil {
		t.Fatalf("a call after the compaction covers the edit, got %+v", o)
	}
}

// The branch window and the agent-deck session obey the same boundary: after
// a /clear the conversation has a new Claude Code session id, the MCP server
// still reports the old one, and only the agent-deck id ties them together.
func TestClearEndsAgentSessionAndBranchCover(t *testing.T) {
	dir := repo(t, "")
	file := filepath.Join(dir, "internal/foo/foo.go")
	deck := map[string]string{usage.AgentSessionEnv: "deck-1"}
	// Before the clear: one call by agent session, one same-branch call.
	logCall(t, dir, usage.Record{Path: "internal/foo", Session: "old", AgentSession: "deck-1", Time: t0.Add(-20 * time.Minute)})
	logCall(t, dir, usage.Record{Path: "internal/foo", Session: "other", Branch: "feat/x", Time: t0.Add(-15 * time.Minute)})
	sessionStart(t, dir, "clear", "new", t0.Add(-10*time.Minute), deck)

	if o := run(t, dir, input(t, dir, "Edit", file, "new"), deck); o == nil {
		t.Fatal("neither the agent-session nor the branch match may cover across a /clear")
	}
	if r := hookRecords(t, dir)[0]; r.LostTo != "clear" {
		t.Errorf("want lost_to clear: %+v", r)
	}
	if !strings.Contains(LostNote("clear"), "cleared") {
		t.Error("the /clear note must say cleared")
	}

	// The MCP server, still on the old session id, calls after the clear.
	logCall(t, dir, usage.Record{Path: "internal/foo", Session: "old", AgentSession: "deck-1", Time: t0.Add(-time.Minute)})
	if o := run(t, dir, input(t, dir, "Edit", file, "new"), deck); o != nil {
		t.Fatalf("a post-clear call matched by agent session covers, got %+v", o)
	}
	if r := hookRecords(t, dir)[1]; r.CoveredBy != "agent_session" {
		t.Errorf("want covered by agent_session: %+v", r)
	}
}

// Another session's boundary ends nothing here, and a session that never
// reset keeps exactly the old coverage.
func TestBoundaryIsPerSession(t *testing.T) {
	dir := repo(t, "")
	file := filepath.Join(dir, "internal/foo/foo.go")
	logCall(t, dir, usage.Record{Path: "internal/foo", Session: "s1", Time: t0.Add(-10 * time.Minute)})
	sessionStart(t, dir, "compact", "s2", t0.Add(-5*time.Minute), nil)
	if o := run(t, dir, input(t, dir, "Edit", file, "s1"), nil); o != nil {
		t.Fatalf("s2's compaction must not end s1's cover, got %+v", o)
	}
}

// Only compact and clear are boundaries: startup has no context to lose and
// resume restores it. A boundary needs someone to belong to, and the gate's
// off modes record nothing.
func TestOnlyResetsRecordABoundary(t *testing.T) {
	dir := repo(t, "")
	for _, src := range []string{"startup", "resume", ""} {
		sessionStart(t, dir, src, "s1", t0, nil)
	}
	sessionStart(t, dir, "compact", "", t0, nil) // no session, no agent session
	SessionStart([]byte("not json"), opts(nil))
	if got := boundaries(t, dir); len(got) != 0 {
		t.Fatalf("want no boundary records, got %+v", got)
	}

	sessionStart(t, dir, "compact", "s1", t0, map[string]string{usage.AgentSessionEnv: "deck-1"})
	got := boundaries(t, dir)
	if len(got) != 1 || got[0].Boundary != "compact" || got[0].Session != "s1" ||
		got[0].AgentSession != "deck-1" || got[0].Branch != "feat/x" {
		t.Fatalf("boundary record: %+v", got)
	}

	for _, cfg := range []string{"context_gate: off\n", "usage:\n  log: off\n"} {
		d := repo(t, "")
		write(t, d, ".nugit/config.yml", cfg)
		sessionStart(t, d, "compact", "s1", t0, nil)
		if got := boundaries(t, d); len(got) != 0 {
			t.Errorf("%q: want no boundary, got %+v", cfg, got)
		}
	}
}
