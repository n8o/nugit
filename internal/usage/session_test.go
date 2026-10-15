package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/retrieval"
)

// Log stamps the Claude Code session and the agent-deck instance from the
// environment the serving process inherited (ADR-0042).
func TestLogStampsSessions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(SessionEnv, "sess-1")
	t.Setenv(AgentSessionEnv, "deck-1")
	if err := Log(dir, SourceMCP, "t", retrieval.Bundle{Path: "a.go"}); err != nil {
		t.Fatal(err)
	}
	got, _ := Read(dir)
	if len(got) != 1 || got[0].Session != "sess-1" || got[0].AgentSession != "deck-1" {
		t.Fatalf("want sessions stamped, got %+v", got)
	}
}

// Unset env leaves both fields empty — and absent from the line, so a record
// from a client that exports no session reads exactly as before.
func TestLogWithoutSessionOmitsFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(SessionEnv, "")
	t.Setenv(AgentSessionEnv, "")
	if err := Log(dir, SourceCLI, "", retrieval.Bundle{Path: "a.go"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, LogPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"session"`, `"agent_session"`, `"covered"`, `"tool"`, `"gate"`} {
		if strings.Contains(string(raw), k) {
			t.Errorf("context record carries %s: %s", k, raw)
		}
	}
}

// A hook record's covered:false must survive the roundtrip (a plain bool with
// omitempty would erase it), and a pre-session line must still parse.
func TestHookRecordRoundtripAndOldLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".nugit", ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"time":"2026-06-01T12:00:00Z","source":"mcp","branch":"b","path":"a.go","component":"c","budget_tokens":4000,"estimated_tokens":9,"truncated":false,"decisions":1,"lessons":0,"glossary":0,"working_memory":0,"spec":false,"dropped":0}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, LogPath), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	f := false
	if err := Append(dir, Record{Time: time.Now().UTC(), Source: SourceHook, Path: "a.go",
		Tool: "Edit", Covered: &f, Gate: "warn", Session: "s"}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil || len(got) != 2 {
		t.Fatalf("want 2 records, got %d (%v)", len(got), err)
	}
	if got[0].Session != "" || got[0].Covered != nil || got[0].Component != "c" {
		t.Errorf("old line: %+v", got[0])
	}
	if got[1].Covered == nil || *got[1].Covered || got[1].Tool != "Edit" || got[1].Session != "s" {
		t.Errorf("hook line: %+v", got[1])
	}
}

func TestFindCover(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	edit := Edit{Time: now, Session: "s1", AgentSession: "d1", Branch: "feat/x",
		Path: "internal/foo/bar.go", AbsPath: "/repo/internal/foo/bar.go", Component: "foo"}
	call := func(mut func(*Record)) Record {
		r := Record{Time: now.Add(-time.Minute), Source: SourceMCP, Path: "internal/other.go",
			Component: "foo", Branch: "feat/x", Session: "s1"}
		mut(&r)
		return r
	}
	cases := []struct {
		name string
		r    Record
		want string
	}{
		{"same session, same component", call(func(*Record) {}), "session"},
		{"same session, path prefix, unmapped", call(func(r *Record) { r.Component = ""; r.Path = "internal/foo" }), "session"},
		{"same session, exact absolute path", call(func(r *Record) { r.Component = ""; r.Path = "/repo/internal/foo/bar.go" }), "session"},
		{"same session, sibling path is not a prefix", call(func(r *Record) { r.Component = ""; r.Path = "internal/fo" }), ""},
		{"same session, repo root covers all", call(func(r *Record) { r.Component = ""; r.Path = "." }), "session"},
		{"same session, other component", call(func(r *Record) { r.Component = "bar" }), ""},
		{"agent-deck match survives a /clear", call(func(r *Record) { r.Session = "s0"; r.AgentSession = "d1" }), "agent_session"},
		{"branch fallback in window", call(func(r *Record) { r.Session = "" }), "branch"},
		{"branch fallback, other session too", call(func(r *Record) { r.Session = "s9" }), "branch"},
		{"branch fallback out of window", call(func(r *Record) { r.Session = ""; r.Time = now.Add(-5 * time.Hour) }), ""},
		{"other branch, no session", call(func(r *Record) { r.Session = ""; r.Branch = "main" }), ""},
		{"detached HEAD never matches by branch", call(func(r *Record) { r.Session = ""; r.Branch = "HEAD" }), ""},
		{"a later call does not cover", call(func(r *Record) { r.Time = now.Add(time.Minute) }), ""},
		{"a hook record is not a read", call(func(r *Record) { r.Source = SourceHook }), ""},
	}
	for _, c := range cases {
		e := edit
		if c.name == "detached HEAD never matches by branch" {
			e.Branch = "HEAD"
		}
		if got := FindCover([]Record{c.r}, e, DefaultCoverWindow); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// The strongest evidence wins regardless of record order.
	rs := []Record{call(func(r *Record) { r.Session = "" }), call(func(*Record) {})}
	if got := FindCover(rs, edit, DefaultCoverWindow); got != "session" {
		t.Errorf("session match must beat a branch match, got %q", got)
	}
}

func TestAggregateEditCoverage(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	yes, no := true, false
	recs := []Record{
		{Time: t0, Source: SourceMCP, Component: "foo", Session: "s1"},
		{Time: t0, Source: SourceHook, Tool: "Edit", Session: "s1", Covered: &yes, CoveredBy: "session"},
		{Time: t0, Source: SourceHook, Tool: "Write", Session: "s1", Covered: &no},
		{Time: t0, Source: SourceHook, Tool: "Edit", AgentSession: "d2", Covered: &no},
		{Time: t0, Source: SourceHook, Tool: "Edit", Covered: &yes, CoveredBy: "branch"},
	}
	s := Aggregate(recs, time.Time{})
	if s.Calls != 1 {
		t.Errorf("hook records must not count as context() calls: calls=%d", s.Calls)
	}
	if s.Edits.Seen != 4 || s.Edits.Covered != 2 || s.Edits.CoveragePct != 50 {
		t.Errorf("edits: %+v", s.Edits)
	}
	if s.Edits.ByCoveredBy["session"] != 1 || s.Edits.ByCoveredBy["branch"] != 1 {
		t.Errorf("by covered_by: %v", s.Edits.ByCoveredBy)
	}
	want := map[string]SessionStats{
		"s1":       {Session: "s1", Calls: 1, Edits: 2, Covered: 1},
		"agent:d2": {Session: "agent:d2", Edits: 1},
		"(none)":   {Session: "(none)", Edits: 1, Covered: 1},
	}
	if len(s.Edits.BySession) != len(want) {
		t.Fatalf("by session: %+v", s.Edits.BySession)
	}
	for _, ss := range s.Edits.BySession {
		if ss != want[ss.Session] {
			t.Errorf("session %s: got %+v, want %+v", ss.Session, ss, want[ss.Session])
		}
	}
	if s.Edits.BySession[0].Session != "s1" {
		t.Errorf("busiest session first, got %s", s.Edits.BySession[0].Session)
	}
	md := s.Markdown()
	for _, frag := range []string{"edit coverage", "edits seen**: 4", "preceded by context()**: 2 (50%)", "s1: 1 · 2 · 1"} {
		if !strings.Contains(md, frag) {
			t.Errorf("markdown missing %q:\n%s", frag, md)
		}
	}
}

// With no session anywhere there is no per-session table, and a log with no
// hook records renders no edit section at all (pre-hook repos are unchanged).
func TestAggregateWithoutSessionsOrEdits(t *testing.T) {
	t0 := time.Now().UTC()
	no := false
	s := Aggregate([]Record{{Time: t0, Source: SourceHook, Covered: &no}}, time.Time{})
	if s.Edits.BySession != nil {
		t.Errorf("no sessions recorded: want no breakdown, got %+v", s.Edits.BySession)
	}
	if !strings.Contains(s.Markdown(), "edits seen**: 1") {
		t.Errorf("edits must render even with zero context() calls:\n%s", s.Markdown())
	}
	s = Aggregate([]Record{{Time: t0, Source: SourceCLI}}, time.Time{})
	if strings.Contains(s.Markdown(), "edit coverage") {
		t.Errorf("no hook records: no edit section expected")
	}
}
