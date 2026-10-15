// Package editgate is the Claude Code PreToolUse hook behind `nugit hook
// pre-edit` (ADR-0042). For each file edit it asks one question — did a
// context() call for this file's component (or path) precede it, in the same
// session or, failing that, on the same branch recently? — records the answer
// in the local usage log, and, when the answer is no, tells the agent to call
// context() first (warn, the default) or refuses the edit (block, opt-in).
//
// It sits on the edit path of a live agent, so its one hard rule is that it
// can never break an edit: any internal error, unparsable input, or panic
// degrades to "allow, say nothing". Deterministic and LLM-free (ADR-0006).
package editgate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/retrieval"
	"github.com/n8o/nugit/internal/usage"
)

// editTools are the Claude Code tools that write a file — the same set the
// installed hook's matcher names (agentcfg.PreEditMatcher).
var editTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// Input is the subset of the PreToolUse hook's stdin JSON the gate reads.
// Edit, Write and MultiEdit name their target `file_path` (always absolute);
// NotebookEdit has used `notebook_path`, so both are accepted.
type Input struct {
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

// Options are the hook's ambient inputs, injectable for tests.
type Options struct {
	// NugitDir is the nugit root relative to the git toplevel (`-C`, default ".").
	NugitDir string
	Now      time.Time
	Getenv   func(string) string
	Window   time.Duration // branch-fallback window; 0 = usage.DefaultCoverWindow
}

// Output is the PreToolUse JSON the hook prints. Warn returns only
// additionalContext — deliberately no permissionDecision, because "allow"
// would also skip the user's permission prompt, and the gate has no business
// approving an edit the user would otherwise have been asked about.
type Output struct {
	HookSpecificOutput HookSpecificOutput `json:"hookSpecificOutput"`
}

// HookSpecificOutput is the PreToolUse-specific half of Output.
type HookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// PreEdit runs the gate over one hook invocation and returns what to print on
// stdout (nil = print nothing, allow). It never returns an error: every
// failure is an allow.
func PreEdit(stdin io.Reader, opt Options) (out []byte) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	var in Input
	if json.NewDecoder(stdin).Decode(&in) != nil || !editTools[in.ToolName] {
		return nil
	}
	o := Evaluate(in, opt)
	if o == nil {
		return nil
	}
	b, err := json.Marshal(o)
	if err != nil {
		return nil
	}
	return b
}

// Evaluate resolves, records and decides one edit. nil = allow silently.
func Evaluate(in Input, opt Options) *Output {
	if opt.Getenv == nil {
		opt.Getenv = os.Getenv
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now().UTC()
	}
	if opt.Window <= 0 {
		opt.Window = usage.DefaultCoverWindow
	}
	file := in.ToolInput.FilePath
	if file == "" {
		file = in.ToolInput.NotebookPath
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if file == "" || cwd == "" {
		return nil
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(cwd, file)
	}
	root, abs, rel, ok := locate(cwd, file)
	if !ok {
		return nil // not a file in a git checkout: not nugit's to judge
	}
	nugitRoot := filepath.Join(root, opt.NugitDir)
	cfg, err := config.Load(nugitRoot)
	if err != nil || cfg.ContextGate == "off" || cfg.Usage.Log == "off" {
		// No log, no measurement: with usage logging off every edit would look
		// uncovered, so the gate stands down rather than nag on false evidence.
		return nil
	}

	session := strings.TrimSpace(in.SessionID)
	if session == "" {
		session = strings.TrimSpace(opt.Getenv(usage.SessionEnv))
	}
	e := usage.Edit{
		Time:         opt.Now,
		Session:      session,
		AgentSession: strings.TrimSpace(opt.Getenv(usage.AgentSessionEnv)),
		Branch:       gitutil.Repo{Dir: root}.CurrentBranch(),
		Path:         rel,
		AbsPath:      abs,
		Component:    retrieval.ResolveComponent(nugitRoot, rel),
	}
	records, _ := usage.Read(root)
	by := usage.FindCover(records, e, opt.Window)
	covered := by != ""
	_ = usage.Append(root, usage.Record{
		Time: e.Time, Source: usage.SourceHook, Branch: e.Branch,
		Session: e.Session, AgentSession: e.AgentSession,
		Path: rel, Component: e.Component, Tool: in.ToolName,
		Covered: &covered, CoveredBy: by, Gate: cfg.ContextGate,
	})
	if covered {
		return nil
	}
	msg := Message(rel, e.Component, root)
	hso := HookSpecificOutput{HookEventName: "PreToolUse"}
	if cfg.ContextGate == "block" {
		hso.PermissionDecision = "deny"
		hso.PermissionDecisionReason = "Blocked by nugit (context_gate: block). " + msg
	} else {
		hso.AdditionalContext = msg
	}
	return &Output{HookSpecificOutput: hso}
}

// Message is the instruction an uncovered edit gets: short, and naming the
// exact call that would have covered it.
func Message(rel, component, root string) string {
	scope := ""
	if component != "" {
		scope = fmt.Sprintf(" (component %s)", component)
	}
	return fmt.Sprintf("nugit: no context() call covers %s%s in this session. "+
		"Before editing it further, call the nugit MCP `context` tool with path %q and cwd %q "+
		"(or run `nugit context -path %s`): it returns the decisions, rejected alternatives, "+
		"lessons and architecture edges that govern this file.", rel, scope, rel, root, rel)
}

// locate resolves the edited file to (worktree root, absolute path, root-
// relative slash path). The root is the checkout that owns the file's nearest
// existing ancestor (a Write may create a new directory) — not the hook's cwd,
// because a linked worktree may live INSIDE the main checkout and the cwd's
// root would then mis-relativize the path. cwd is the fallback. Symlinks are
// resolved on both sides so /tmp vs /private/tmp style aliases still compare.
func locate(cwd, file string) (root, abs, rel string, ok bool) {
	abs = resolveExisting(filepath.Clean(file))
	for _, start := range []string{nearestDir(abs), cwd} {
		top, err := gitutil.Repo{Dir: start}.WorktreeRoot()
		if err != nil || top == "" {
			continue
		}
		top = resolveExisting(top)
		r, err := filepath.Rel(top, abs)
		if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			continue
		}
		return top, abs, filepath.ToSlash(r), true
	}
	return "", "", "", false
}

// resolveExisting evaluates symlinks on the longest existing prefix of p and
// re-appends the rest, so a file that does not exist yet still resolves.
func resolveExisting(p string) string {
	var tail []string
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{r}, tail...)...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}

func nearestDir(p string) string {
	for d := filepath.Dir(p); ; d = filepath.Dir(d) {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
		if filepath.Dir(d) == d {
			return d
		}
	}
}
