package verify

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/usage"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

func cfg(gate string) config.Config {
	c, _ := config.LoadBytes([]byte("verify:\n  gate: " + gate + "\n"))
	return c
}

func ev(at time.Time, session, kind string) usage.Record {
	return usage.Record{Time: at, Source: usage.SourceEvidence, Session: session, Evidence: kind}
}

func edit(at time.Time, session string) usage.Record {
	return usage.Record{Time: at, Source: usage.SourceHook, Session: session, Path: "a.go"}
}

func commitRec(at time.Time, session, verdict string) usage.Record {
	return usage.Record{Time: at, Source: usage.SourceCommit, Session: session, Verdict: verdict}
}

const msg = "feat: x\n\nbody\n"

func TestEvidenceInTheSessionStampsTheCommit(t *testing.T) {
	recs := []usage.Record{
		edit(t0, "s1"),
		ev(t0.Add(time.Minute), "s1", "e2e"),
		ev(t0.Add(2*time.Minute), "s1", "tests"),
		ev(t0.Add(2*time.Minute), "other", "review"), // another session's review is not ours
	}
	d := Decide(cfg("enforce"), Commit{Message: msg, Session: "s1", Time: t0.Add(3 * time.Minute)}, recs)
	if d.Verdict != usage.VerdictVerified || d.Block || d.Stamp != "verified: e2e, tests" {
		t.Fatalf("got %+v", d)
	}
}

// A check that ran before the tree changed verified a different tree.
func TestEvidenceBeforeTheLatestEditIsStale(t *testing.T) {
	recs := []usage.Record{
		ev(t0, "s1", "tests"),
		edit(t0.Add(time.Minute), "s1"),
	}
	d := Decide(cfg("observe"), Commit{Message: msg, Session: "s1", Time: t0.Add(2 * time.Minute)}, recs)
	if d.Verdict != usage.VerdictWouldBlock || d.Block || d.Stamp != "verified: none" {
		t.Fatalf("stale evidence must not count: %+v", d)
	}
	d = Decide(cfg("enforce"), Commit{Message: msg, Session: "s1", Time: t0.Add(2 * time.Minute)}, recs)
	if !d.Block || !strings.Contains(d.Message, "before its latest edit") {
		t.Fatalf("enforce must block and say the evidence is stale: %+v", d)
	}
}

// Observe stamps `verified: none` and records what enforce would have done,
// and says nothing: its job is to measure without changing behaviour.
func TestObserveNeverBlocksOrSpeaks(t *testing.T) {
	d := Decide(cfg("observe"), Commit{Message: msg, Session: "s1", Time: t0}, nil)
	if d.Block || d.Message != "" || d.Verdict != usage.VerdictWouldBlock || d.Stamp != "verified: none" {
		t.Fatalf("got %+v", d)
	}
}

func TestEnforceBlocksThenReleasesAfterTwo(t *testing.T) {
	c := Commit{Message: msg, AgentSession: "deck-1", Time: t0}
	d := Decide(cfg("enforce"), c, nil)
	if !d.Block || d.Verdict != usage.VerdictBlock || d.Stamp != "" {
		t.Fatalf("first unverified commit blocks: %+v", d)
	}
	recs := []usage.Record{
		{Time: t0, Source: usage.SourceCommit, AgentSession: "deck-1", Verdict: usage.VerdictBlock},
	}
	c.Time = t0.Add(time.Minute)
	if d := Decide(cfg("enforce"), c, recs); !d.Block {
		t.Fatalf("one block is not enough to release: %+v", d)
	}
	recs = append(recs, usage.Record{Time: t0.Add(time.Minute), Source: usage.SourceCommit, AgentSession: "deck-1", Verdict: usage.VerdictBlock})
	c.Time = t0.Add(2 * time.Minute)
	d = Decide(cfg("enforce"), c, recs)
	if d.Block || d.Verdict != usage.VerdictRelease || !strings.Contains(d.Stamp, "released after 2 blocked commits") {
		t.Fatalf("the third attempt goes through, stamped as released: %+v", d)
	}
	// The count restarts after a commit goes through.
	recs = append(recs, usage.Record{Time: c.Time, Source: usage.SourceCommit, AgentSession: "deck-1", Verdict: usage.VerdictRelease})
	c.Time = t0.Add(3 * time.Minute)
	if d := Decide(cfg("enforce"), c, recs); !d.Block {
		t.Fatalf("after a release the next unverified commit blocks again: %+v", d)
	}
}

func TestAReasonLetsTheCommitThrough(t *testing.T) {
	m := "docs: fix a typo\n\nverified: none — docs-only change\n"
	d := Decide(cfg("enforce"), Commit{Message: m, Session: "s1", Time: t0}, nil)
	if d.Block || d.Verdict != usage.VerdictExplained || d.Stamp != "" {
		t.Fatalf("a reason is enough: %+v", d)
	}
	// A bare claim with no evidence and no note is not a reason.
	m = "feat: y\n\nverified: tests\n"
	if d := Decide(cfg("enforce"), Commit{Message: m, Session: "s1", Time: t0}, nil); !d.Block {
		t.Fatalf("an unbacked bare claim blocks under enforce: %+v", d)
	}
}

// A person at a terminal has no session evidence to read; nugit does not
// guess how they checked their work.
func TestNoSessionMeansNoGate(t *testing.T) {
	if d := Decide(cfg("enforce"), Commit{Message: msg, Time: t0}, nil); d.Verdict != "" || d.Block || d.Stamp != "" {
		t.Fatalf("got %+v", d)
	}
	if d := Decide(cfg("off"), Commit{Message: msg, Session: "s1", Time: t0}, nil); d.Verdict != "" {
		t.Fatalf("off: got %+v", d)
	}
}

func TestClassify(t *testing.T) {
	c := config.Default()
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	for _, tc := range []struct {
		event, tool string
		in, resp    any
		prompt      string
		kind        string
	}{
		{"PostToolUse", "Skill", map[string]string{"skill": "verify"}, nil, "", "e2e"},
		{"PostToolUse", "Skill", map[string]string{"skill": "web:verify-checkout"}, nil, "", "e2e"},
		{"PostToolUse", "Skill", map[string]string{"skill": "code-review"}, nil, "", "review"},
		{"PostToolUse", "Skill", map[string]string{"skill": "simplify"}, nil, "", ""},
		{"PostToolUse", "Agent", map[string]string{"subagent_type": "service-reviewer"}, nil, "", "review"},
		{"PostToolUse", "Agent", map[string]string{"subagent_type": "Explore"}, nil, "", ""},
		{"PostToolUse", "Bash", map[string]string{"command": "cd api && GOFLAGS=-count=1 go test ./..."}, map[string]any{"stdout": "ok"}, "", "tests"},
		{"PostToolUse", "Bash", map[string]string{"command": "go vet ./..."}, nil, "", ""},
		{"PostToolUse", "Bash", map[string]string{"command": "go test ./..."}, map[string]any{"interrupted": true}, "", ""},
		{"PostToolUse", "Bash", map[string]string{"command": "go test ./..."}, map[string]any{"stderr": "Exit code 1\nFAIL"}, "", ""},
		{"UserPromptSubmit", "", nil, nil, "/verify the checkout flow", "e2e"},
		{"UserPromptSubmit", "", nil, nil, "/code-review high", "review"},
		{"UserPromptSubmit", "", nil, nil, "please verify it", ""},
		{"PreToolUse", "Skill", map[string]string{"skill": "verify"}, nil, "", ""},
	} {
		kind, _ := Classify(tc.event, tc.tool, raw(tc.in), raw(tc.resp), tc.prompt, c)
		if kind != tc.kind {
			t.Errorf("%s %s %v %q: kind=%q, want %q", tc.event, tc.tool, tc.in, tc.prompt, kind, tc.kind)
		}
	}
}

func TestAppendTrailer(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"feat: x\n", "feat: x\n\nverified: e2e\n"},
		{"feat: x\n\nbody\n", "feat: x\n\nbody\n\nverified: e2e\n"},
		{"feat: x\n\nbody\n\nobjective: V-1\nCo-Authored-By: A <a@b>\n", "feat: x\n\nbody\n\nobjective: V-1\nCo-Authored-By: A <a@b>\nverified: e2e\n"},
		{"feat: x\n\n# Please enter the commit message\n# lines starting with #\n", "feat: x\n\nverified: e2e\n\n# Please enter the commit message\n# lines starting with #\n"},
	} {
		if got := appendTrailer(tc.in, "verified: e2e"); got != tc.want {
			t.Errorf("appendTrailer(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "feat/x"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "root"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// End to end through the log: the evidence hook records a verify-skill run,
// and the commit-msg hook stamps the next commit from it.
func TestEvidenceHookThenCommitMsg(t *testing.T) {
	dir := gitInit(t)
	env := map[string]string{usage.SessionEnv: "s1"}
	opt := Options{Now: t0, Getenv: func(k string) string { return env[k] }}
	in, _ := json.Marshal(map[string]any{
		"session_id": "s1", "cwd": dir, "hook_event_name": "PostToolUse",
		"tool_name": "Skill", "tool_input": map[string]string{"skill": "verify"},
	})
	if r := Evidence(bytes.NewReader(in), opt); r == nil || r.Evidence != "e2e" {
		t.Fatalf("evidence not recorded: %+v", r)
	}
	f := filepath.Join(dir, "MSG")
	if err := os.WriteFile(f, []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	opt.Now = t0.Add(time.Minute)
	var stderr bytes.Buffer
	if code := CommitMsg(dir, f, &stderr, opt); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	b, _ := os.ReadFile(f)
	if !strings.HasSuffix(string(b), "\n\nverified: e2e\n") {
		t.Errorf("message not stamped: %q", b)
	}
	recs, _ := usage.Read(dir)
	if len(recs) != 2 || recs[1].Source != usage.SourceCommit || recs[1].Verdict != usage.VerdictVerified {
		t.Errorf("want an evidence and a commit record, got %+v", recs)
	}
	s := usage.Aggregate(recs, time.Time{})
	if s.Calls != 0 || s.Verify.Commits != 1 || s.Verify.Verified != 1 || s.Verify.Evidence["e2e"] != 1 {
		t.Errorf("stats must count the gate, not context calls: %+v", s)
	}
}

// Under enforce the hook exits non-zero and the message is untouched.
func TestCommitMsgBlocks(t *testing.T) {
	dir := gitInit(t)
	if err := os.MkdirAll(filepath.Join(dir, ".nugit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".nugit", "config.yml"), []byte("verify:\n  gate: enforce\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "MSG")
	_ = os.WriteFile(f, []byte(msg), 0o644)
	var stderr bytes.Buffer
	env := map[string]string{usage.SessionEnv: "s1"}
	code := CommitMsg(dir, f, &stderr, Options{Now: t0, Getenv: func(k string) string { return env[k] }})
	if code != 1 || !strings.Contains(stderr.String(), "verified: none — docs-only change") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if b, _ := os.ReadFile(f); string(b) != msg {
		t.Errorf("a blocked message is left as written: %q", b)
	}
}

// `git merge` runs the commit-msg hook too, with its message file in the git
// dir beside MERGE_HEAD, and so does the `git commit` that concludes a
// conflicted merge. The PR view skips merge commits (they carry no work of
// their own), so the hook does too: under enforce an agent's merge of the
// default branch is never blocked, and no merge is stamped `verified: none`.
func TestCommitMsgSkipsAMerge(t *testing.T) {
	dir := gitInit(t)
	c := cfg("enforce")
	gitDir := filepath.Join(dir, ".git")
	f := filepath.Join(gitDir, "MERGE_MSG")
	merge := "Merge branch 'main' into feat/x\n"
	_ = os.WriteFile(f, []byte(merge), 0o644)
	if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{usage.SessionEnv: "s1"}
	var stderr bytes.Buffer
	if code := CommitMsg(dir, f, &stderr, Options{Now: t0, Config: &c, Getenv: func(k string) string { return env[k] }}); code != 0 {
		t.Fatalf("a merge was blocked: code=%d stderr=%s", code, stderr.String())
	}
	if b, _ := os.ReadFile(f); string(b) != merge {
		t.Errorf("a merge message was stamped: %q", b)
	}
	if recs, _ := usage.Read(dir); len(recs) != 0 {
		t.Errorf("a merge was recorded as a commit verdict: %+v", recs)
	}
	// The same message file with no merge in progress is an ordinary commit.
	_ = os.Remove(filepath.Join(gitDir, "MERGE_HEAD"))
	if code := CommitMsg(dir, f, &stderr, Options{Now: t0, Config: &c, Getenv: func(k string) string { return env[k] }}); code != 1 {
		t.Errorf("with no merge in progress the gate applies: code=%d", code)
	}
}
