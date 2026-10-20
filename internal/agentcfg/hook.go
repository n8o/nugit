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

// SessionStartMatcher is the SessionStart matcher of the installed digest:
// every way a session begins or loses its context (ADR-0048).
const SessionStartMatcher = "startup|resume|clear|compact"

// sessionStartTimeout bounds the digest in seconds; it reads local files and
// two git refs, and a timed-out SessionStart hook only drops the digest.
const sessionStartTimeout = 10

// SessionStartCommand is the digest hook command for bin ("" = "nugit").
func SessionStartCommand(bin string) string {
	if bin == "" {
		bin = "nugit"
	}
	return bin + " hook session-start"
}

// IsSessionStartCommand reports whether a hook command runs nugit's digest.
func IsSessionStartCommand(cmd string) bool {
	return strings.Contains(cmd, "nugit") && strings.Contains(cmd, "hook session-start")
}

// hookSpec is one hook nugit installs.
type hookSpec struct {
	event, matcher, command string
	timeout                 int
	ours                    func(cmd string) bool
}

func specs(bin string) []hookSpec {
	return []hookSpec{
		{"PreToolUse", PreEditMatcher, PreEditCommand(bin), preEditTimeout, IsPreEditCommand},
		{"SessionStart", SessionStartMatcher, SessionStartCommand(bin), sessionStartTimeout, IsSessionStartCommand},
	}
}

// InstallClaudeCodeHook merges nugit's Claude Code hooks into
// repoDir/.claude/settings.json: the pre-edit PreToolUse gate (ADR-0042) and
// the SessionStart objective digest (ADR-0048). Unlike .mcp.json,
// settings.json is a file a project already owns for permissions, env and
// other hooks, so this MERGES: every existing key, every other hook entry, and
// the file's key order are kept; each event gains nugit's entry only when it
// has none. Nothing missing → changed is false and nothing is written; added
// names the events that gained an entry. A file that is not a JSON object (or
// whose `hooks`, or an event under it, has the wrong shape) is an error and is
// never rewritten.
func InstallClaudeCodeHook(repoDir, bin string) (changed bool, path string, err error) {
	added, path, err := InstallClaudeCodeHooks(repoDir, bin)
	return len(added) > 0, path, err
}

// InstallClaudeCodeHooks is InstallClaudeCodeHook, reporting which events
// gained an entry.
func InstallClaudeCodeHooks(repoDir, bin string) (added []string, path string, err error) {
	path = filepath.Join(repoDir, ".claude", "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, path, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	top, err := decodeObject(raw)
	if err != nil {
		return nil, path, fmt.Errorf("%s: %w (left untouched)", path, err)
	}
	hooks := &object{vals: map[string]json.RawMessage{}}
	if h, ok := top.vals["hooks"]; ok {
		if hooks, err = decodeObject(h); err != nil {
			return nil, path, fmt.Errorf("%s: hooks: %w (left untouched)", path, err)
		}
	}
	type command struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	for _, sp := range specs(bin) {
		var entries []json.RawMessage
		if p, ok := hooks.vals[sp.event]; ok {
			if err := json.Unmarshal(p, &entries); err != nil {
				return nil, path, fmt.Errorf("%s: hooks.%s is not an array (left untouched)", path, sp.event)
			}
		}
		if installed(entries, sp.ours) {
			continue
		}
		entry, err := json.Marshal(struct {
			Matcher string    `json:"matcher"`
			Hooks   []command `json:"hooks"`
		}{sp.matcher, []command{{"command", sp.command, sp.timeout}}})
		if err != nil {
			return nil, path, err
		}
		b, err := json.Marshal(append(entries, entry))
		if err != nil {
			return nil, path, err
		}
		hooks.set(sp.event, b)
		added = append(added, sp.event)
	}
	if len(added) == 0 {
		return nil, path, nil
	}
	top.set("hooks", hooks.encode())

	var out bytes.Buffer
	if err := json.Indent(&out, top.encode(), "", "  "); err != nil {
		return nil, path, err
	}
	out.WriteByte('\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, path, err
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return nil, path, fmt.Errorf("writing %s: %w", path, err)
	}
	return added, path, nil
}

// HookInstalled reports whether any PreToolUse entry runs `nugit hook pre-edit`.
func HookInstalled(preToolUse []json.RawMessage) bool {
	return installed(preToolUse, IsPreEditCommand)
}

// SessionStartInstalled reports whether any SessionStart entry runs
// `nugit hook session-start`.
func SessionStartInstalled(sessionStart []json.RawMessage) bool {
	return installed(sessionStart, IsSessionStartCommand)
}

func installed(entries []json.RawMessage, ours func(string) bool) bool {
	for _, e := range entries {
		var entry struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(e, &entry) != nil {
			continue
		}
		for _, h := range entry.Hooks {
			if ours(h.Command) {
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
