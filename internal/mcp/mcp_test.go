package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
