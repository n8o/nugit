package agentcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSettings(t *testing.T, dir string) (string, map[string]any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("installed settings.json is not JSON: %v\n%s", err, b)
	}
	return string(b), m
}

func preToolUse(t *testing.T, m map[string]any) []any {
	t.Helper()
	hooks, _ := m["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	return pre
}

func TestInstallHookCreatesSettings(t *testing.T) {
	dir := t.TempDir()
	changed, path, err := InstallClaudeCodeHook(dir, "")
	if err != nil || !changed || path != filepath.Join(dir, ".claude", "settings.json") {
		t.Fatalf("got changed=%v path=%s err=%v", changed, path, err)
	}
	_, m := readSettings(t, dir)
	pre := preToolUse(t, m)
	if len(pre) != 1 {
		t.Fatalf("want one PreToolUse entry, got %v", pre)
	}
	e := pre[0].(map[string]any)
	h := e["hooks"].([]any)[0].(map[string]any)
	if e["matcher"] != PreEditMatcher || h["type"] != "command" || h["command"] != "nugit hook pre-edit" {
		t.Errorf("entry: %v", e)
	}
}

// The merge keeps every foreign key, every other hook (on PreToolUse and on
// other events), and the file's key order; a second install is a no-op.
func TestInstallHookMergesWithoutClobbering(t *testing.T) {
	dir := t.TempDir()
	orig := `{
  "permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {
    "PostToolUse": [{"matcher": "Write", "hooks": [{"type": "command", "command": "gofmt-it"}]}],
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh"}]}]
  },
  "env": {"FOO": "1"}
}
`
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, _, err := InstallClaudeCodeHook(dir, "/opt/bin/nugit")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	text, m := readSettings(t, dir)
	if p := m["permissions"].(map[string]any)["allow"].([]any); len(p) != 1 || p[0] != "Bash(go test:*)" {
		t.Errorf("permissions lost: %v", m["permissions"])
	}
	if m["env"].(map[string]any)["FOO"] != "1" {
		t.Errorf("env lost")
	}
	if post := m["hooks"].(map[string]any)["PostToolUse"].([]any); len(post) != 1 {
		t.Errorf("PostToolUse hook lost")
	}
	pre := preToolUse(t, m)
	if len(pre) != 2 || pre[0].(map[string]any)["matcher"] != "Bash" {
		t.Fatalf("foreign PreToolUse entry must stay first: %v", pre)
	}
	if !strings.Contains(text, `"command": "/opt/bin/nugit hook pre-edit"`) {
		t.Errorf("bin not embedded:\n%s", text)
	}
	if !(strings.Index(text, `"permissions"`) < strings.Index(text, `"hooks"`) &&
		strings.Index(text, `"hooks"`) < strings.Index(text, `"env"`) &&
		strings.Index(text, `"PostToolUse"`) < strings.Index(text, `"PreToolUse"`)) {
		t.Errorf("key order not preserved:\n%s", text)
	}

	changed, _, err = InstallClaudeCodeHook(dir, "")
	if err != nil || changed {
		t.Fatalf("second install must be a no-op: changed=%v err=%v", changed, err)
	}
	if text2, _ := readSettings(t, dir); text2 != text {
		t.Errorf("no-op install rewrote the file")
	}
}

// A settings file nugit cannot parse as an object is reported, never rewritten.
func TestInstallHookRefusesUnparsableSettings(t *testing.T) {
	for name, body := range map[string]string{
		"not json":        `{"permissions": `,
		"array":           `[]`,
		"hooks not obj":   `{"hooks": []}`,
		"pre not array":   `{"hooks": {"PreToolUse": {}}}`,
		"trailing object": `{} {}`,
	} {
		dir := t.TempDir()
		p := filepath.Join(dir, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := InstallClaudeCodeHook(dir, ""); err == nil {
			t.Errorf("%s: want an error", name)
		}
		if b, _ := os.ReadFile(p); string(b) != body {
			t.Errorf("%s: file was modified: %s", name, b)
		}
	}
}

func TestIsPreEditCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"nugit hook pre-edit":                true,
		"/usr/local/bin/nugit hook pre-edit": true,
		"nugit hook commit-msg":              false,
		"other hook pre-edit":                false,
	} {
		if IsPreEditCommand(cmd) != want {
			t.Errorf("%q: want %v", cmd, want)
		}
	}
}

// ADR-0048: the SessionStart digest installs alongside the pre-edit gate, and
// a repo that already has the gate gains only the digest.
func TestInstallHooksAddsSessionStart(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) {
		if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"hooks": {
  "PreToolUse": [{"matcher": "Edit", "hooks": [{"type": "command", "command": "nugit hook pre-edit"}]}],
  "SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "other.sh"}]}]
}}`)
	added, _, err := InstallClaudeCodeHooks(dir, "")
	if err != nil || strings.Join(added, ",") != "SessionStart" {
		t.Fatalf("added = %v, err = %v; want only SessionStart", added, err)
	}
	_, m := readSettings(t, dir)
	hooks := m["hooks"].(map[string]any)
	ss := hooks["SessionStart"].([]any)
	if len(ss) != 2 || len(preToolUse(t, m)) != 1 {
		t.Fatalf("hooks = %v", hooks)
	}
	e := ss[1].(map[string]any)
	h := e["hooks"].([]any)[0].(map[string]any)
	if e["matcher"] != SessionStartMatcher || h["command"] != "nugit hook session-start" {
		t.Errorf("entry: %v", e)
	}
	if added, _, _ := InstallClaudeCodeHooks(dir, ""); len(added) != 0 {
		t.Errorf("second install added %v", added)
	}
	write(`{"hooks": {"SessionStart": {}}}`)
	if _, _, err := InstallClaudeCodeHooks(dir, ""); err == nil {
		t.Error("a non-array SessionStart was rewritten")
	}
}
