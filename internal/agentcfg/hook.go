package agentcfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PreEditMatcher is the PreToolUse matcher of the installed hook — the tools
// that write a file (kept equal to editgate.Matcher; agentcfg renders config
// text and does not import the gate).
const PreEditMatcher = "Edit|Write|MultiEdit|NotebookEdit"

// preEditTimeout bounds the hook in seconds. A timed-out PreToolUse hook does
// not block the tool call, so this only caps how long an edit can wait.
const preEditTimeout = 10

// PreEditCommand is the hook command for bin ("" = "nugit").
func PreEditCommand(bin string) string {
	if bin == "" {
		bin = "nugit"
	}
	return bin + " hook pre-edit"
}

// IsPreEditCommand reports whether a hook command runs nugit's pre-edit gate.
func IsPreEditCommand(cmd string) bool {
	return strings.Contains(cmd, "nugit") && strings.Contains(cmd, "hook pre-edit")
}

// InstallClaudeCodeHook merges the pre-edit PreToolUse hook (ADR-0042) into
// repoDir/.claude/settings.json. Unlike .mcp.json, settings.json is a file a
// project already owns for permissions, env and other hooks, so this MERGES:
// every existing key, every other hook entry, and the file's key order are
// kept; only hooks.PreToolUse gains one entry. Already installed → changed is
// false and nothing is written. A file that is not a JSON object (or whose
// `hooks` is not one) is an error and is never rewritten.
func InstallClaudeCodeHook(repoDir, bin string) (changed bool, path string, err error) {
	path = filepath.Join(repoDir, ".claude", "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, path, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	top, err := decodeObject(raw)
	if err != nil {
		return false, path, fmt.Errorf("%s: %w (left untouched)", path, err)
	}
	hooks := &object{vals: map[string]json.RawMessage{}}
	if h, ok := top.vals["hooks"]; ok {
		if hooks, err = decodeObject(h); err != nil {
			return false, path, fmt.Errorf("%s: hooks: %w (left untouched)", path, err)
		}
	}
	var pre []json.RawMessage
	if p, ok := hooks.vals["PreToolUse"]; ok {
		if err := json.Unmarshal(p, &pre); err != nil {
			return false, path, fmt.Errorf("%s: hooks.PreToolUse is not an array (left untouched)", path)
		}
	}
	if HookInstalled(pre) {
		return false, path, nil
	}
	type command struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	entry, err := json.Marshal(struct {
		Matcher string    `json:"matcher"`
		Hooks   []command `json:"hooks"`
	}{PreEditMatcher, []command{{"command", PreEditCommand(bin), preEditTimeout}}})
	if err != nil {
		return false, path, err
	}
	pre = append(pre, entry)
	preRaw, err := json.Marshal(pre)
	if err != nil {
		return false, path, err
	}
	hooks.set("PreToolUse", preRaw)
	top.set("hooks", hooks.encode())

	var out bytes.Buffer
	if err := json.Indent(&out, top.encode(), "", "  "); err != nil {
		return false, path, err
	}
	out.WriteByte('\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, path, err
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return false, path, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, path, nil
}

// HookInstalled reports whether any PreToolUse entry runs `nugit hook pre-edit`.
func HookInstalled(preToolUse []json.RawMessage) bool {
	for _, e := range preToolUse {
		var entry struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(e, &entry) != nil {
			continue
		}
		for _, h := range entry.Hooks {
			if IsPreEditCommand(h.Command) {
				return true
			}
		}
	}
	return false
}

// object is a JSON object that remembers its key order, so a merge rewrites a
// user's settings.json without reshuffling it. Values stay raw (untouched).
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func decodeObject(b []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	o := &object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(k, v)
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the JSON object")
	}
	return o, nil
}

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) encode() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}
