// Package mcp exposes nugit's context() retrieval as an MCP tool over stdio, so
// Claude Code / agents can fetch a scoped, typed knowledge bundle for a path.
// Minimal newline-delimited JSON-RPC 2.0 — no SDK, deterministic, no datastore.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"runtime/debug"

	"github.com/n8o/nugit/internal/retrieval"
	"github.com/n8o/nugit/internal/usage"
)

const protocolVersion = "2024-11-05"

// serverVersion reports the module's build version so serverInfo matches
// `nugit version` instead of a hand-maintained constant that drifts on every
// release (it said 0.1.0 while v0.2.0 shipped).
func serverVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "devel"
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve runs the stdio MCP loop until in is exhausted. It uses a bufio.Reader
// (not a Scanner) so an arbitrarily large LLM-facing request never overflows a
// token-size cap and tears down the session.
func Serve(repoDir string, in io.Reader, out io.Writer) error {
	rd := bufio.NewReader(in)
	enc := json.NewEncoder(out)
	for {
		line, err := rd.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			var req request
			if json.Unmarshal(t, &req) == nil && len(req.ID) != 0 {
				if e := enc.Encode(handle(repoDir, req)); e != nil {
					return e
				}
			}
			// a notification (no id) or unparseable line: no reply, keep going
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func handle(repoDir string, req request) response {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "nugit", "version": serverVersion()},
		}
	case "tools/list":
		resp.Result = map[string]interface{}{"tools": []interface{}{contextTool()}}
	case "tools/call":
		resp.Result, resp.Error = callTool(repoDir, req.Params)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

func contextTool() map[string]interface{} {
	return map[string]interface{}{
		"name":        "context",
		"description": "Scoped, typed knowledge bundle for a file/dir path: the C4 architecture slice, in-scope decisions (with their rejected rationale), the active spec, matched lessons, matched references (distilled external sources), and glossary — bounded by a token budget.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path":          map[string]interface{}{"type": "string", "description": "file or dir the agent is operating on"},
				"task":          map[string]interface{}{"type": "string", "description": "current task, for keyword matching"},
				"budget_tokens": map[string]interface{}{"type": "integer", "description": "hard cap on returned size"},
			},
			"required": []string{"path"},
		},
	}
}

func callTool(repoDir string, params json.RawMessage) (interface{}, *rpcError) {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Path         string `json:"path"`
			Task         string `json:"task"`
			BudgetTokens int    `json:"budget_tokens"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	if p.Name != "context" {
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
	b, err := retrieval.Context(retrieval.Options{
		RepoDir: repoDir, Path: p.Arguments.Path, Task: p.Arguments.Task, BudgetTokens: p.Arguments.BudgetTokens,
	})
	if err != nil {
		return toolError(err.Error()), nil
	}
	// Best-effort local usage log; a logging failure must never fail the tool call.
	_ = usage.Log(repoDir, "mcp", p.Arguments.Task, b)
	js, _ := json.Marshal(b)
	return map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": string(js)},
		},
	}, nil
}

func toolError(msg string) interface{} {
	return map[string]interface{}{
		"isError": true,
		"content": []interface{}{map[string]interface{}{"type": "text", "text": msg}},
	}
}
