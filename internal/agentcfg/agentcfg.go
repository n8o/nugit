// Package agentcfg emits the per-client MCP wiring config that launches
// `nugit mcp` from a coding agent (`nugit agent`). It closes the adoption gap
// where capture works out of the box but retrieval stays unwired because the
// user never tells their client about the MCP server. Deterministic, no
// network — it renders config text; the only write is the claude-code
// project-scoped .mcp.json install.
package agentcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Client identifies a coding agent whose MCP wiring config we can emit.
type Client string

const (
	ClaudeCode Client = "claude-code"
	Cursor     Client = "cursor"
	Codex      Client = "codex"
	OpenCode   Client = "opencode"
	Generic    Client = "generic"
)

// Clients lists every supported client, in help-text order.
func Clients() []Client {
	return []Client{ClaudeCode, Cursor, Codex, OpenCode, Generic}
}

// server is the mcpServers entry shape shared by claude-code, cursor and
// generic clients (field order matches .mcp.json.example).
type server struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Snippet returns the exact config text for client plus a one-line hint of
// where it goes. bin == "" means "nugit" (resolved from PATH by the client).
//
// Project-scoped configs (claude-code, generic, opencode) use a relative
// `-C "."` — the client launches the server from the project root. User-global
// configs (cursor, codex) bake the resolved absolute repoDir into -C, because
// "." would resolve to whatever cwd the client happens to launch from.
//
// Git-worktree fan-out needs NO per-worktree wiring: one server started from
// any checkout serves every linked worktree, because the context tool accepts
// a per-request `cwd` and resolves the repo root/branch from it (ADR-0025).
func Snippet(client Client, repoDir, bin string) (text, dest string, err error) {
	if bin == "" {
		bin = "nugit"
	}
	switch client {
	case ClaudeCode, Generic:
		text, err = marshalJSON(map[string]map[string]server{
			"mcpServers": {"nugit": {Command: bin, Args: []string{"mcp", "-C", "."}}},
		})
		if client == ClaudeCode {
			dest = ".mcp.json at the repo root (project-scoped; Claude Code loads it on restart)"
		} else {
			dest = ".mcp.json at the repo root (or your client's MCP config, same shape)"
		}
		return text, dest, err
	case Cursor:
		abs, aerr := filepath.Abs(repoDir)
		if aerr != nil {
			return "", "", aerr
		}
		text, err = marshalJSON(map[string]map[string]server{
			"mcpServers": {"nugit": {Command: bin, Args: []string{"mcp", "-C", abs}}},
		})
		return text, "~/.cursor/mcp.json (user-global, hence the absolute path)", err
	case Codex:
		abs, aerr := filepath.Abs(repoDir)
		if aerr != nil {
			return "", "", aerr
		}
		text = fmt.Sprintf("[mcp_servers.nugit]\ncommand = %q\nargs = [\"mcp\", \"-C\", %q]\n", bin, abs)
		return text, "~/.codex/config.toml (user-global, hence the absolute path)", nil
	case OpenCode:
		text, err = marshalJSON(map[string]map[string]struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
		}{
			"mcp": {"nugit": {Type: "local", Command: []string{bin, "mcp", "-C", "."}}},
		})
		return text, "opencode.json in the project", err
	default:
		return "", "", fmt.Errorf("unknown client %q (valid: %s)", client, clientList())
	}
}

// InstallClaudeCode writes the project-scoped .mcp.json into repoDir. If the
// file already exists and force is unset it returns created=false with no
// error — the caller prints the snippet for a manual merge. It NEVER merges
// into an existing file (a foreign .mcp.json may carry other servers; a
// deterministic tool should not rewrite it).
func InstallClaudeCode(repoDir, bin string, force bool) (created bool, path string, err error) {
	path = filepath.Join(repoDir, ".mcp.json")
	if _, serr := os.Stat(path); serr == nil && !force {
		return false, path, nil
	}
	text, _, err := Snippet(ClaudeCode, repoDir, bin)
	if err != nil {
		return false, path, err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return false, path, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, path, nil
}

// marshalJSON renders v as two-space-indented JSON with a trailing newline —
// the same formatting as .mcp.json.example, so installed and hand-merged
// configs look alike.
func marshalJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

func clientList() string {
	s := ""
	for i, c := range Clients() {
		if i > 0 {
			s += ", "
		}
		s += string(c)
	}
	return s
}
