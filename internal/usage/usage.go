// Package usage records context() calls to a local, gitignored JSONL log and
// aggregates it for `nugit stats`. This is the first rung of measuring whether
// retrieval actually helps agents: before impact can be measured, use has to be
// observable at all. The log never leaves the repo (.nugit/.cache/ is
// gitignored), and logging is best-effort by design — a logging failure must
// never break or delay the bundle a caller is waiting on (ADR-0013).
package usage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/retrieval"
)

// LogPath is where records land, relative to the repo's COMMON root. It lives
// under .nugit/.cache/ — the designated rebuildable, gitignored local dir; the
// canonical store stays git text only.
const LogPath = ".nugit/.cache/usage.jsonl"

// logFile resolves the log location against the common working-tree root (the
// main checkout) so every linked worktree of a repo appends to ONE log and
// `nugit stats` sees all calls regardless of which worktree served them
// (ADR-0025). Outside a git repo it falls back to repoDir unchanged. For a
// plain single-worktree repo the resolved path is byte-identical to before.
func logFile(repoDir string) string {
	return filepath.Join(gitutil.Repo{Dir: repoDir}.CommonRoot(), LogPath)
}

// Record is one served context() call, or (Source == SourceHook) one edit the
// pre-edit hook saw (ADR-0042). Every field added after the first release is
// omitempty, so an old line still parses and a new line stays readable by an
// older nugit.
type Record struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"` // cli | mcp | hook
	Branch string    `json:"branch,omitempty"`
	// Session is the Claude Code session id the call or edit belongs to ("" =
	// unknown: another client, or a Claude Code that does not export it).
	// AgentSession is the agent-deck instance id, a second, independent
	// attribution that survives a Claude Code /clear (ADR-0042).
	Session      string `json:"session,omitempty"`
	AgentSession string `json:"agent_session,omitempty"`
	// Hook records only: the editing tool, whether a prior context() call
	// covered the edit, which attribution matched it, and the gate mode.
	Tool            string `json:"tool,omitempty"`
	Covered         *bool  `json:"covered,omitempty"`
	CoveredBy       string `json:"covered_by,omitempty"` // session | agent_session | branch
	Gate            string `json:"gate,omitempty"`       // warn | block
	Path            string `json:"path"`
	Task            string `json:"task,omitempty"`
	Component       string `json:"component"` // "" = path resolved to no component
	BudgetTokens    int    `json:"budget_tokens"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Truncated       bool   `json:"truncated"`
	Decisions       int    `json:"decisions"`
	Lessons         int    `json:"lessons"`
	References      int    `json:"references"`
	Glossary        int    `json:"glossary"`
	WorkingMemory   int    `json:"working_memory"`
	PathHistory     int    `json:"path_history"`
	Spec            bool   `json:"spec"`
	Dropped         int    `json:"dropped"`
	// Delivery (ADR-0043): whether the requested budget was clamped to fit the
	// output window, what was asked for, the served JSON's size in characters
	// (0 = not measured, e.g. the CLI), and how many in-scope items the
	// relevance cap omitted.
	Clamped         bool `json:"clamped,omitempty"`
	RequestedBudget int  `json:"requested_budget,omitempty"`
	Chars           int  `json:"chars,omitempty"`
	Omitted         int  `json:"omitted,omitempty"`
}

// FromBundle summarizes a served bundle into a Record.
func FromBundle(source, task string, b retrieval.Bundle) Record {
	return Record{
		Time:            time.Now().UTC(),
		Source:          source,
		Path:            b.Path,
		Task:            task,
		Component:       b.Component,
		BudgetTokens:    b.BudgetTokens,
		EstimatedTokens: b.EstimatedTokens,
		Truncated:       b.Truncated,
		Decisions:       len(b.Decisions),
		Lessons:         len(b.Lessons),
		References:      len(b.References),
		Glossary:        len(b.Glossary),
		WorkingMemory:   len(b.WorkingMemory),
		PathHistory:     len(b.PathHistory),
		Spec:            b.Spec != nil,
		Dropped:         droppedCount(b),
		Clamped:         b.Clamped,
		RequestedBudget: b.RequestedBudgetTokens,
		Chars:           b.Chars,
		Omitted:         omittedCount(b),
	}
}

func droppedCount(b retrieval.Bundle) int {
	if b.DroppedCount > 0 {
		return b.DroppedCount
	}
	return len(b.Dropped)
}

func omittedCount(b retrieval.Bundle) int {
	n := 0
	for _, v := range b.Omitted {
		n += v
	}
	return n
}

// Log records a served bundle unless config.yml says `usage: {log: off}`.
// Callers deliberately ignore the returned error on the serving path.
func Log(repoDir, source, task string, b retrieval.Bundle) error {
	cfg, _ := config.Load(repoDir)
	if cfg.Usage.Log == "off" {
		return nil
	}
	r := FromBundle(source, task, b)
	// Best-effort branch stamp: it is what lets per-branch usage be joined
	// against per-branch outcomes (pr-render findings). "" (not a git repo)
	// and "HEAD" (detached) are both acceptable — never an error. repoDir is
	// the PER-REQUEST resolved root (the worktree actually being edited), so
	// under worktree fan-out each record carries that worktree's branch, not
	// the primary checkout's (ADR-0025).
	r.Branch = gitutil.Repo{Dir: repoDir}.CurrentBranch()
	r.Session, r.AgentSession = SessionFromEnv()
	return Append(repoDir, r)
}

// Source values.
const (
	SourceCLI  = "cli"
	SourceMCP  = "mcp"
	SourceHook = "hook"
)

// SessionEnv is the variable Claude Code sets, to the current session id, in
// the environment of the processes it spawns: Bash tool commands and stdio MCP
// servers alike. AgentSessionEnv is agent-deck's per-session instance id.
// Both are read best-effort: unset means "unattributed", never an error.
const (
	SessionEnv      = "CLAUDE_CODE_SESSION_ID"
	AgentSessionEnv = "AGENTDECK_INSTANCE_ID"
)

// SessionFromEnv returns the (Claude Code session, agent-deck instance) ids of
// the current process environment.
func SessionFromEnv() (session, agentSession string) {
	return strings.TrimSpace(os.Getenv(SessionEnv)), strings.TrimSpace(os.Getenv(AgentSessionEnv))
}

// Append writes one record to the shared log, creating .nugit/.cache/ at the
// common root if needed.
func Append(repoDir string, r Record) error {
	p := logFile(repoDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// Read loads all records from the shared log. A missing log yields (nil, nil);
// malformed lines are skipped (a half-written line from a crashed process must
// not poison stats).
func Read(repoDir string) ([]Record, error) {
	f, err := os.Open(logFile(repoDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil && !r.Time.IsZero() {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

// Stats is the aggregate over a set of records.
type Stats struct {
	Calls        int            `json:"calls"`
	First        time.Time      `json:"first,omitempty"`
	Last         time.Time      `json:"last,omitempty"`
	BySource     map[string]int `json:"by_source"`
	ByComponent  map[string]int `json:"by_component"`
	ByBranch     map[string]int `json:"by_branch"`
	Unresolved   int            `json:"unresolved"`    // no component matched: model coverage gap
	EmptyBundles int            `json:"empty_bundles"` // no decisions/spec/lessons matched: capture gap
	Truncated    int            `json:"truncated"`
	AvgTokens    int            `json:"avg_estimated_tokens"`
	// Edits is what the pre-edit hook saw (ADR-0042); zero until it is installed.
	Edits EditStats `json:"edits"`
}

// EditStats measures edit-time coverage: how many edits the pre-edit hook saw
// and how many a context() call for that path preceded.
type EditStats struct {
	Seen        int            `json:"seen"`
	Covered     int            `json:"covered"`
	CoveragePct float64        `json:"coverage_pct"`
	ByCoveredBy map[string]int `json:"by_covered_by,omitempty"` // session | agent_session | branch
	// BySession is present only when some record carries a session id.
	BySession []SessionStats `json:"by_session,omitempty"`
}

// SessionStats is one session's context() calls against its edits.
type SessionStats struct {
	Session string `json:"session"` // Claude Code session id, or "agent:<id>", or "(none)"
	Calls   int    `json:"calls"`
	Edits   int    `json:"edits"`
	Covered int    `json:"covered"`
}

// sessionKey is the per-session breakdown's grouping key: the Claude Code
// session when known, else the agent-deck instance, else "(none)".
func sessionKey(r Record) string {
	switch {
	case r.Session != "":
		return r.Session
	case r.AgentSession != "":
		return "agent:" + r.AgentSession
	}
	return "(none)"
}

// Aggregate computes Stats over records at or after since (zero = all).
func Aggregate(records []Record, since time.Time) Stats {
	s := Stats{BySource: map[string]int{}, ByComponent: map[string]int{}, ByBranch: map[string]int{}}
	s.Edits.ByCoveredBy = map[string]int{}
	tokens := 0
	sessions := map[string]*SessionStats{}
	attributed := false
	session := func(r Record) *SessionStats {
		k := sessionKey(r)
		if k != "(none)" {
			attributed = true
		}
		if sessions[k] == nil {
			sessions[k] = &SessionStats{Session: k}
		}
		return sessions[k]
	}
	for _, r := range records {
		if !since.IsZero() && r.Time.Before(since) {
			continue
		}
		if r.Source == SourceHook {
			ss := session(r)
			s.Edits.Seen++
			ss.Edits++
			if r.Covered != nil && *r.Covered {
				s.Edits.Covered++
				ss.Covered++
				if r.CoveredBy != "" {
					s.Edits.ByCoveredBy[r.CoveredBy]++
				}
			}
			continue
		}
		session(r).Calls++
		s.Calls++
		if s.First.IsZero() || r.Time.Before(s.First) {
			s.First = r.Time
		}
		if r.Time.After(s.Last) {
			s.Last = r.Time
		}
		s.BySource[r.Source]++
		if r.Component == "" {
			s.Unresolved++
		} else {
			s.ByComponent[r.Component]++
		}
		if r.Branch != "" {
			s.ByBranch[r.Branch]++
		}
		if r.Decisions == 0 && r.Lessons == 0 && !r.Spec {
			s.EmptyBundles++
		}
		if r.Truncated {
			s.Truncated++
		}
		tokens += r.EstimatedTokens
	}
	if s.Calls > 0 {
		s.AvgTokens = tokens / s.Calls
	}
	if s.Edits.Seen > 0 {
		s.Edits.CoveragePct = float64(s.Edits.Covered) * 100 / float64(s.Edits.Seen)
	}
	if attributed {
		for _, ss := range sessions {
			s.Edits.BySession = append(s.Edits.BySession, *ss)
		}
		sort.Slice(s.Edits.BySession, func(i, j int) bool {
			a, b := s.Edits.BySession[i], s.Edits.BySession[j]
			if a.Edits != b.Edits {
				return a.Edits > b.Edits
			}
			if a.Calls != b.Calls {
				return a.Calls > b.Calls
			}
			return a.Session < b.Session
		})
	}
	return s
}

// Markdown renders the stats for the terminal.
func (s Stats) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## nugit stats — context() usage\n\n")
	if s.Calls == 0 {
		b.WriteString("_No context() calls recorded. The log fills as agents call " +
			"`nugit context` / the MCP `context` tool (local only: `" + LogPath + "`; " +
			"disable with `usage: {log: off}`)._\n")
		s.writeEdits(&b)
		return b.String()
	}
	fmt.Fprintf(&b, "- **calls**: %d (%s → %s)\n", s.Calls,
		s.First.Format("2006-01-02"), s.Last.Format("2006-01-02"))
	for _, src := range sortedKeys(s.BySource) {
		fmt.Fprintf(&b, "  - via %s: %d\n", src, s.BySource[src])
	}
	fmt.Fprintf(&b, "- **avg bundle size**: ~%d tokens; truncated: %d/%d\n", s.AvgTokens, s.Truncated, s.Calls)
	fmt.Fprintf(&b, "- **unresolved paths** (no C4 component owns them — model coverage gap): %d\n", s.Unresolved)
	fmt.Fprintf(&b, "- **empty bundles** (no decision/spec/lesson matched — capture gap): %d\n", s.EmptyBundles)
	if len(s.ByComponent) > 0 {
		b.WriteString("- **top components**:\n")
		for _, t := range topN(s.ByComponent, 10) {
			fmt.Fprintf(&b, "  - %s: %d\n", t.k, t.v)
		}
	}
	if len(s.ByBranch) > 0 {
		b.WriteString("- **by branch**:\n")
		for _, t := range topN(s.ByBranch, 10) {
			fmt.Fprintf(&b, "  - %s: %d\n", t.k, t.v)
		}
	}
	s.writeEdits(&b)
	return b.String()
}

// maxSessionRows caps the per-session table; the JSON output carries them all.
const maxSessionRows = 15

// writeEdits renders edit-time coverage (ADR-0042). Silent until the
// pre-edit hook has recorded something, so a repo without it sees no change.
func (s Stats) writeEdits(b *strings.Builder) {
	e := s.Edits
	if e.Seen == 0 {
		return
	}
	b.WriteString("\n### edit coverage (pre-edit hook)\n\n")
	fmt.Fprintf(b, "- **edits seen**: %d\n", e.Seen)
	fmt.Fprintf(b, "- **preceded by context()**: %d (%.0f%%)\n", e.Covered, e.CoveragePct)
	for _, k := range sortedKeys(e.ByCoveredBy) {
		fmt.Fprintf(b, "  - matched by %s: %d\n", k, e.ByCoveredBy[k])
	}
	if len(e.BySession) == 0 {
		return
	}
	b.WriteString("- **by session** (calls · edits · covered):\n")
	for i, ss := range e.BySession {
		if i == maxSessionRows {
			fmt.Fprintf(b, "  - … %d more (see `-format json`)\n", len(e.BySession)-maxSessionRows)
			break
		}
		fmt.Fprintf(b, "  - %s: %d · %d · %d\n", ss.Session, ss.Calls, ss.Edits, ss.Covered)
	}
}

type kv struct {
	k string
	v int
}

// topN returns the n highest-count entries of m, ties broken alphabetically.
func topN(m map[string]int, n int) []kv {
	var top []kv
	for k, v := range m {
		top = append(top, kv{k, v})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].v != top[j].v {
			return top[i].v > top[j].v
		}
		return top[i].k < top[j].k
	})
	if len(top) > n {
		top = top[:n]
	}
	return top
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
