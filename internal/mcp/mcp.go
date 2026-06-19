// Package mcp exposes nugit's context() retrieval as an MCP tool over stdio, so
// Claude Code / agents can fetch a scoped, typed knowledge bundle for a path.
// Minimal newline-delimited JSON-RPC 2.0 — no SDK, deterministic, no datastore.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"

	"github.com/n8o/nugit/internal/retrieval"
)

const protocolVersion = "2024-11-05"

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

// Serve runs the stdio MCP loop until in is exhausted.
func Serve(repoDir string, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification (e.g. notifications/initialized) — no reply
		}
		if err := enc.Encode(handle(repoDir, req)); err != nil {
			return err
		}
	}
	return sc.Err()
}

func handle(repoDir string, req request) response {
	resp := response{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "nugit", "version": "0.1.0"},
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
		"description": "Scoped, typed knowledge bundle for a file/dir path: the C4 architecture slice, in-scope decisions (with their rejected rationale), the active spec, matched lessons, and glossary — bounded by a token budget.",
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
