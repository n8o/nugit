package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/usage"
)

func wf(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Drive a full initialize -> tools/list -> tools/call(context) round-trip over
// the stdio protocol and assert each response.
func TestMCPRoundTrip(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, ".nugit/architecture/workspace.dsl", `workspace "m" {
  model { sys = softwareSystem "m" {
    render = component "R" { properties { paths "internal/render/**" } }
  } }
}`)
	wf(t, dir, ".nugit/decisions/r.md",
		"---\nschema_version: 1\nid: ADR-R\ntype: decision\nscope: render\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# render decision\n")

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // notification: no reply
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"context","arguments":{"path":"internal/render/render.go"}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := Serve(dir, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}

	var replies []map[string]json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad reply line %q: %v", line, err)
		}
		replies = append(replies, m)
	}
	// 3 requests with ids -> 3 replies; the notification produced none.
	if len(replies) != 3 {
		t.Fatalf("want 3 replies (notification suppressed), got %d", len(replies))
	}
	// initialize advertises the protocol version
	if !strings.Contains(string(replies[0]["result"]), protocolVersion) {
		t.Errorf("initialize missing protocolVersion: %s", replies[0]["result"])
	}
	// tools/list advertises the context tool
	if !strings.Contains(string(replies[1]["result"]), `"context"`) {
		t.Errorf("tools/list missing context tool: %s", replies[1]["result"])
	}
	// tools/call returns a content bundle naming the resolved component
	call := string(replies[2]["result"])
	if !strings.Contains(call, `"content"`) || !strings.Contains(call, "render") {
		t.Errorf("tools/call bundle wrong: %s", call)
	}
}

// gitDo runs git in dir with identity/signing pinned so the test is hermetic.
func gitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

const wtDSL = `workspace "m" {
  model { sys = softwareSystem "m" {
    render = component "R" { properties { paths "internal/render/**" } }
  } }
}`

func decisionMD(id string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: decision\nscope: render\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# " + id + " decision\n"
}

// One server process, started from the PRIMARY checkout, must serve each
// request from the worktree named by the request's cwd: the bundle reads THAT
// checkout's knowledge and the usage record carries THAT worktree's branch —
// not the primary's. A cwd outside the repo falls back to the start root and
// never serves a foreign repo's knowledge (ADR-0025).
func TestPerRequestCwdResolvesWorktree(t *testing.T) {
	tmp := t.TempDir()
	main := filepath.Join(tmp, "repo")
	wt := filepath.Join(tmp, "wt")
	other := filepath.Join(tmp, "other")

	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDo(t, main, "init")
	wf(t, main, ".nugit/architecture/workspace.dsl", wtDSL)
	wf(t, main, ".nugit/decisions/main.md", decisionMD("ADR-MAIN"))
	gitDo(t, main, "add", "-A")
	gitDo(t, main, "commit", "-m", "seed")
	gitDo(t, main, "checkout", "-B", "main")
	gitDo(t, main, "worktree", "add", wt, "-b", "feat/wt")
	// The worktree's checkout diverges: it carries a decision the primary
	// checkout does not have.
	wf(t, wt, ".nugit/decisions/wt.md", decisionMD("ADR-WT"))

	// An unrelated repo with its own store — a cross-repo cwd must NOT reach it.
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDo(t, other, "init")
	wf(t, other, ".nugit/architecture/workspace.dsl", wtDSL)
	wf(t, other, ".nugit/decisions/other.md", decisionMD("ADR-OTHER"))

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"context","arguments":{"path":"internal/render/render.go"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"context","arguments":{"path":"internal/render/render.go","cwd":` + mustJSON(t, wt) + `}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"context","arguments":{"path":"internal/render/render.go","cwd":` + mustJSON(t, other) + `}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := Serve(main, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 replies, got %d: %s", len(lines), out.String())
	}

	// No cwd -> the primary checkout: ADR-MAIN only.
	if !strings.Contains(lines[0], "ADR-MAIN") || strings.Contains(lines[0], "ADR-WT") {
		t.Errorf("no-cwd request must serve the primary checkout: %s", lines[0])
	}
	// cwd=worktree -> the WORKTREE's checkout: its extra decision appears.
	if !strings.Contains(lines[1], "ADR-WT") {
		t.Errorf("cwd request must serve the worktree's checkout: %s", lines[1])
	}
	// cwd in a DIFFERENT repo -> guarded fallback to the start root.
	if strings.Contains(lines[2], "ADR-OTHER") || !strings.Contains(lines[2], "ADR-MAIN") {
		t.Errorf("cross-repo cwd must fall back to the start root: %s", lines[2])
	}

	// Usage: one shared log at the primary checkout, per-request branches.
	recs, err := usage.Read(main)
	if err != nil || len(recs) != 3 {
		t.Fatalf("usage.Read(main): want 3 records, got %d (err %v)", len(recs), err)
	}
	if recs[0].Branch != "main" || recs[1].Branch != "feat/wt" || recs[2].Branch != "main" {
		t.Errorf("per-request branch attribution wrong: [%q %q %q]",
			recs[0].Branch, recs[1].Branch, recs[2].Branch)
	}
	if _, err := os.Stat(filepath.Join(wt, ".nugit", ".cache")); !os.IsNotExist(err) {
		t.Errorf("worktree grew its own usage cache (err=%v)", err)
	}
}

// mustJSON encodes s as a JSON string literal (paths may contain backslashes).
func mustJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
