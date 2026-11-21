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

	"github.com/n8o/nugit/internal/claim"
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
	cfg, err := LocalConfig(nugitRoot)
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
	remind := ""
	if firstEditOnBranch(records, e) {
		remind = claim.Reminder(nugitRoot)
	}
	covered := by != ""
	lost := ""
	if !covered {
		lost = usage.LostCover(records, e, opt.Window)
	}
	_ = usage.Append(root, usage.Record{
		Time: e.Time, Source: usage.SourceHook, Branch: e.Branch,
		Session: e.Session, AgentSession: e.AgentSession,
		Path: rel, Component: e.Component, Tool: in.ToolName,
		Covered: &covered, CoveredBy: by, Gate: cfg.ContextGate, LostTo: lost,
	})
	if covered && remind == "" {
		return nil
	}
	hso := HookSpecificOutput{HookEventName: "PreToolUse"}
	var context []string
	if !covered {
		msg := Message(rel, e.Component, root)
		if lost != "" {
			msg += " " + LostNote(lost)
		}
		if cfg.ContextGate == "block" {
			hso.PermissionDecision = "deny"
			hso.PermissionDecisionReason = "Blocked by nugit (context_gate: block). " + msg
		} else {
			context = append(context, msg)
		}
	}
	if remind != "" {
		context = append(context, remind)
	}
	hso.AdditionalContext = strings.Join(context, "\n\n")
	return &Output{HookSpecificOutput: hso}
}

// firstEditOnBranch reports whether e is the session's first recorded edit on
// its branch, which is when an unclaimed branch gets its one reminder. With no
// session id there is no "once" to keep, so it never fires.
func firstEditOnBranch(records []usage.Record, e usage.Edit) bool {
	if e.Session == "" || e.Branch == "" || e.Branch == "HEAD" {
		return false
	}
	for _, r := range records {
		if r.Source == usage.SourceHook && r.Session == e.Session && r.Branch == e.Branch {
			return false
		}
	}
	return true
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

// LostNote explains an uncovered edit that an earlier call would have
// covered (ADR-0054): the agent remembers calling context(), and needs to know
// why that call stopped counting.
func LostNote(boundary string) string {
	what := "compacted"
	if boundary == "clear" {
		what = "cleared"
	}
	return "Your earlier context() call for it no longer counts: this session's context was " + what +
		" after it, so that bundle is no longer in your window."
}

// SessionStartInput is the subset of the SessionStart hook's stdin JSON the
// boundary reads.
type SessionStartInput struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
	Source    string `json:"source"` // startup | resume | clear | compact
}

// SessionStart records a context boundary when a session's window was
// compacted or cleared (ADR-0054), so the edit gate stops counting calls made
// before it. Like PreEdit it can never break a session: bad input, a non-reset
// source, a path outside a checkout, logging or the gate turned off, or a
// panic all record nothing.
func SessionStart(stdin []byte, opt Options) {
	defer func() { _ = recover() }()
	if opt.Getenv == nil {
		opt.Getenv = os.Getenv
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now().UTC()
	}
	var in SessionStartInput
	if json.Unmarshal(stdin, &in) != nil || !usage.EndsContext(in.Source) {
		return
	}
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if cwd == "" {
		return
	}
	root, err := gitutil.Repo{Dir: cwd}.WorktreeRoot()
	if err != nil || root == "" {
		return
	}
	cfg, err := LocalConfig(filepath.Join(root, opt.NugitDir))
	if err != nil || cfg.ContextGate == "off" || cfg.Usage.Log == "off" {
		return // the gate records nothing, so it has no cover to end
	}
	session := strings.TrimSpace(in.SessionID)
	if session == "" {
		session = strings.TrimSpace(opt.Getenv(usage.SessionEnv))
	}
	agent := strings.TrimSpace(opt.Getenv(usage.AgentSessionEnv))
	if session == "" && agent == "" {
		return // a boundary nobody can be matched to ends nothing
	}
	_ = usage.Append(root, usage.Record{
		Time: opt.Now, Source: usage.SourceBoundary, Branch: gitutil.Repo{Dir: root}.CurrentBranch(),
		Session: session, AgentSession: agent, Boundary: in.Source,
	})
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

// targetReadTimeout bounds the hook's read of the target's config. The hook
// runs before every edit and every commit, so a slow git must cost at most
// this, after which the checkout's config applies as it did before ADR-0055.
const targetReadTimeout = 500 * time.Millisecond

// LocalConfig is the config a local hook is governed by (ADR-0055): the
// checkout's, with every governed key read from origin's default branch, so
// an agent cannot turn a gate off by editing the file next to the one the
// gate stopped it editing. Without a remote default it is the checkout's.
// The commit-msg hook uses it too, which is why it is exported.
func LocalConfig(nugitRoot string) (config.Config, error) {
	repo := gitutil.Repo{Dir: nugitRoot}
	return config.LoadLocal(nugitRoot, func() (string, bool) {
		return repo.DefaultBranchFile(".nugit/config.yml", targetReadTimeout)
	})
}
