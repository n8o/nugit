package editgate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/usage"
)

func gitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}
	if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
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

// repo builds a git repo on branch feat/x with a two-component model and the
// given context_gate mode ("" = no config.yml: the default).
func repo(t *testing.T, gate string) string {
	t.Helper()
	dir := t.TempDir()
	gitDo(t, dir, "init", "-b", "feat/x")
	write(t, dir, ".nugit/architecture/workspace.dsl", `workspace "m" {
  model { sys = softwareSystem "m" {
    foo = component "F" { properties { paths "internal/foo/**" } }
    bar = component "B" { properties { paths "internal/bar/**" } }
  } }
}`)
	if gate != "" {
		write(t, dir, ".nugit/config.yml", "context_gate: "+gate+"\n")
	}
	write(t, dir, "internal/foo/foo.go", "package foo\n")
	gitDo(t, dir, "add", "-A")
	gitDo(t, dir, "commit", "-m", "root")
	return dir
}

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func opts(env map[string]string) Options {
	return Options{NugitDir: ".", Now: t0, Getenv: func(k string) string { return env[k] }}
}

func input(t *testing.T, dir, tool, file, session string) string {
	t.Helper()
	in := map[string]any{
		"session_id": session, "cwd": dir, "hook_event_name": "PreToolUse",
		"tool_name": tool, "tool_input": map[string]any{"file_path": file, "old_string": "a", "new_string": "b"},
		"tool_use_id": "toolu_1", "permission_mode": "default",
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func run(t *testing.T, dir, stdin string, env map[string]string) *Output {
	t.Helper()
	out := PreEdit(strings.NewReader(stdin), opts(env))
	if out == nil {
		return nil
	}
	var o Output
	if err := json.Unmarshal(out, &o); err != nil {
		t.Fatalf("hook printed invalid JSON %q: %v", out, err)
	}
	return &o
}

func hookRecords(t *testing.T, dir string) []usage.Record {
	t.Helper()
	rs, err := usage.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []usage.Record
	for _, r := range rs {
		if r.Source == usage.SourceHook {
			out = append(out, r)
		}
	}
	return out
}

func logCall(t *testing.T, dir string, r usage.Record) {
	t.Helper()
	if r.Time.IsZero() {
		r.Time = t0.Add(-time.Minute)
	}
	if r.Source == "" {
		r.Source = usage.SourceMCP
	}
	if err := usage.Append(dir, r); err != nil {
		t.Fatal(err)
	}
}

// Default (warn): an uncovered edit is allowed, recorded covered:false, and
// Claude gets additionalContext naming the call to make — with NO permission
// decision, so the user's own permission prompt is untouched.
func TestWarnUncoveredEdit(t *testing.T) {
	dir := repo(t, "")
	file := filepath.Join(dir, "internal/foo/foo.go")
	o := run(t, dir, input(t, dir, "Edit", file, "s1"), map[string]string{usage.AgentSessionEnv: "deck-1"})
	if o == nil {
		t.Fatal("warn mode: want a message for an uncovered edit")
	}
	h := o.HookSpecificOutput
	if h.HookEventName != "PreToolUse" || h.PermissionDecision != "" {
		t.Errorf("warn must not make a permission decision: %+v", h)
	}
	if !strings.Contains(h.AdditionalContext, "internal/foo/foo.go") || !strings.Contains(h.AdditionalContext, "component foo") {
		t.Errorf("message must name the path and component: %q", h.AdditionalContext)
	}
	rs := hookRecords(t, dir)
	if len(rs) != 1 {
		t.Fatalf("want 1 hook record, got %d", len(rs))
	}
	r := rs[0]
	if r.Covered == nil || *r.Covered || r.Tool != "Edit" || r.Path != "internal/foo/foo.go" ||
		r.Component != "foo" || r.Session != "s1" || r.AgentSession != "deck-1" || r.Branch != "feat/x" || r.Gate != "warn" {
		t.Errorf("hook record: %+v", r)
	}
}

// A context() call in the same session for the same component covers the edit:
// silent allow, recorded covered:true by session.
func TestCoveredBySession(t *testing.T) {
	dir := repo(t, "warn")
	logCall(t, dir, usage.Record{Path: "internal/foo", Component: "foo", Session: "s1", Branch: "other"})
	if o := run(t, dir, input(t, dir, "Write", filepath.Join(dir, "internal/foo/new.go"), "s1"), nil); o != nil {
		t.Fatalf("covered edit must be silent, got %+v", o)
	}
	r := hookRecords(t, dir)[0]
	if r.Covered == nil || !*r.Covered || r.CoveredBy != "session" {
		t.Errorf("want covered by session: %+v", r)
	}
}

// Hook stdin has the session; with it absent the hook falls back to
// CLAUDE_CODE_SESSION_ID from its own environment.
func TestSessionFromEnvFallback(t *testing.T) {
	dir := repo(t, "")
	logCall(t, dir, usage.Record{Path: "internal/foo/foo.go", Session: "s-env", Branch: "nope"})
	if o := run(t, dir, input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), ""),
		map[string]string{usage.SessionEnv: "s-env"}); o != nil {
		t.Fatalf("want covered via env session, got %+v", o)
	}
}

// No session in common: a same-branch call inside the window covers; outside
// it does not; a call for another component never does.
func TestBranchFallbackAndScope(t *testing.T) {
	dir := repo(t, "")
	logCall(t, dir, usage.Record{Path: "internal/bar/b.go", Component: "bar", Branch: "feat/x"})
	if o := run(t, dir, input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil); o == nil {
		t.Fatal("a call for another component must not cover")
	}
	logCall(t, dir, usage.Record{Path: "internal/foo/foo.go", Component: "foo", Branch: "feat/x", Time: t0.Add(-5 * time.Hour)})
	if o := run(t, dir, input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil); o == nil {
		t.Fatal("a branch call outside the window must not cover")
	}
	logCall(t, dir, usage.Record{Path: "internal/foo/foo.go", Component: "foo", Branch: "feat/x"})
	if o := run(t, dir, input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil); o != nil {
		t.Fatalf("a recent same-branch call must cover, got %+v", o)
	}
	rs := hookRecords(t, dir)
	if len(rs) != 3 || rs[2].CoveredBy != "branch" {
		t.Errorf("want third edit covered by branch: %+v", rs)
	}
}

func TestBlockDenies(t *testing.T) {
	dir := repo(t, "block")
	o := run(t, dir, input(t, dir, "MultiEdit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil)
	if o == nil || o.HookSpecificOutput.PermissionDecision != "deny" ||
		!strings.Contains(o.HookSpecificOutput.PermissionDecisionReason, "context_gate: block") {
		t.Fatalf("block mode must deny with a reason: %+v", o)
	}
	if o.HookSpecificOutput.AdditionalContext != "" {
		t.Errorf("deny carries its message in the reason only")
	}
	if r := hookRecords(t, dir); len(r) != 1 || r[0].Gate != "block" {
		t.Errorf("blocked edits are still recorded: %+v", r)
	}
	// Once covered, block lets the edit through silently.
	logCall(t, dir, usage.Record{Path: "internal/foo/foo.go", Session: "s1"})
	if o := run(t, dir, input(t, dir, "MultiEdit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil); o != nil {
		t.Fatalf("covered edit must pass block mode: %+v", o)
	}
}

// off — and usage.log: off, which leaves nothing to measure against — record
// nothing and say nothing.
func TestOffModesRecordNothing(t *testing.T) {
	for _, cfg := range []string{"context_gate: off\n", "usage:\n  log: off\n"} {
		dir := repo(t, "")
		write(t, dir, ".nugit/config.yml", cfg)
		if o := run(t, dir, input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), "s1"), nil); o != nil {
			t.Errorf("%q: want silence, got %+v", cfg, o)
		}
		if rs, _ := usage.Read(dir); len(rs) != 0 {
			t.Errorf("%q: want nothing recorded, got %+v", cfg, rs)
		}
	}
}

// NotebookEdit's path may arrive as notebook_path; a relative path resolves
// against the hook's cwd.
func TestNotebookPathAndRelativePath(t *testing.T) {
	dir := repo(t, "")
	in := `{"session_id":"s1","cwd":"` + dir + `","tool_name":"NotebookEdit","tool_input":{"notebook_path":"internal/foo/nb.ipynb"}}`
	if o := run(t, dir, in, nil); o == nil {
		t.Fatal("want a warning for the notebook edit")
	}
	if r := hookRecords(t, dir); len(r) != 1 || r[0].Path != "internal/foo/nb.ipynb" || r[0].Component != "foo" {
		t.Errorf("notebook record: %+v", r)
	}
}

// An edit in a linked worktree nested INSIDE the main checkout is relativized
// to the worktree, carries the worktree's branch, and lands in the one shared
// log — even when the hook's cwd is the main checkout.
func TestNestedWorktree(t *testing.T) {
	dir := repo(t, "")
	wt := filepath.Join(dir, ".worktrees", "wt")
	gitDo(t, dir, "worktree", "add", wt, "-b", "feat/wt")
	logCall(t, dir, usage.Record{Path: "internal/foo", Component: "foo", Session: "s1"})
	if o := run(t, dir, input(t, dir, "Edit", filepath.Join(wt, "internal/foo/foo.go"), "s1"), nil); o != nil {
		t.Fatalf("want covered, got %+v", o)
	}
	r := hookRecords(t, dir)
	if len(r) != 1 || r[0].Path != "internal/foo/foo.go" || r[0].Branch != "feat/wt" {
		t.Errorf("worktree record: %+v", r)
	}
}

// The hook never breaks an edit: junk input, non-edit tools, and files
// outside any git checkout are all a silent allow with nothing recorded.
func TestNeverBreaksTheEdit(t *testing.T) {
	dir := repo(t, "block")
	outside := t.TempDir()
	for name, stdin := range map[string]string{
		"empty":        "",
		"not json":     "{nope",
		"wrong shape":  `{"tool_name": 7}`,
		"bash":         input(t, dir, "Bash", filepath.Join(dir, "internal/foo/foo.go"), "s1"),
		"no path":      `{"tool_name":"Edit","cwd":"` + dir + `","tool_input":{}}`,
		"outside repo": input(t, outside, "Write", filepath.Join(outside, "x.go"), "s1"),
	} {
		if out := PreEdit(strings.NewReader(stdin), opts(nil)); out != nil {
			t.Errorf("%s: want silent allow, got %s", name, out)
		}
	}
	if rs, _ := usage.Read(dir); len(rs) != 0 {
		t.Errorf("nothing should be recorded, got %+v", rs)
	}
	// A broken config.yml is an internal error, not a reason to block.
	write(t, dir, ".nugit/config.yml", "context_gate: [unclosed\n")
	if out := PreEdit(strings.NewReader(input(t, dir, "Edit", filepath.Join(dir, "internal/foo/foo.go"), "s1")), opts(nil)); out != nil {
		t.Errorf("malformed config: want silent allow, got %s", out)
	}
}
