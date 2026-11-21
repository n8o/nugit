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
	Tool string `json:"tool,omitempty"`
	// Boundary records only (ADR-0054): the SessionStart source that ended
	// the session's context, "compact" or "clear".
	Boundary string `json:"boundary,omitempty"`
	// LostTo, on an uncovered hook record, names the boundary that ended the
	// cover of a call that would otherwise have covered the edit (ADR-0054).
	LostTo          string `json:"lost_to,omitempty"`
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
	// Cited counts the served items the queried file names by id (ADR-0053):
	// how often the strongest relevance signal was the code's own pointer.
	Cited int `json:"cited,omitempty"`
	// Verification (ADR-0056). An evidence record names the kind of check that
	// ran (e2e | tests | review) and what ran it (a skill, an agent type, a
	// command). A commit record carries the gate's verdict on one commit and
	// the kinds it was stamped with.
	Evidence string   `json:"evidence,omitempty"`
	Via      string   `json:"via,omitempty"`
	Verdict  string   `json:"verdict,omitempty"`
	Kinds    []string `json:"kinds,omitempty"`
}

// IsCall reports whether r is a context() call, as opposed to one of the
// records other hooks append to the same log: an edit, a context boundary, a
// verification, or a commit-gate verdict. Everything that counts calls or
// looks for a covering call goes through it, so a new record kind cannot be
// mistaken for context served.
func (r Record) IsCall() bool {
	switch r.Source {
	case SourceHook, SourceBoundary, SourceEvidence, SourceCommit:
		return false
	}
	return true
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
		Cited:           citedCount(b),
	}
}

// citedCount is how many served items the queried file names (ADR-0053).
func citedCount(b retrieval.Bundle) int {
	n := 0
	for _, sec := range [][]retrieval.Item{b.Decisions, b.Lessons, b.References, b.Contracts} {
		for _, it := range sec {
			if it.Cited {
				n++
			}
		}
	}
	if b.Spec != nil && b.Spec.Cited {
		n++
	}
	return n
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
	// The same governed config the gates read (ADR-0055): if the writer read
	// the checkout and the pre-edit gate read the default branch, a checkout
	// that sets `usage.log: off` would stop every call being logged while the
	// gate went on demanding one.
	cfg, _ := LocalConfig(repoDir)
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

// TargetReadTimeout bounds the read of the default branch's config. The
// callers sit on the edit, commit and context() paths, so a slow git costs at
// most this, after which the checkout's config applies as before ADR-0055.
// It is a variable so the tests of a package that reads governed config
// through LocalConfig can lift it: a test that origin's value governs must not
// also require a loaded machine to answer git inside 500ms.
var TargetReadTimeout = 500 * time.Millisecond

// LocalConfig is the config the local hooks and the usage log are governed by
// (ADR-0055): the checkout's, with every governed key read from origin's
// default branch, so an agent cannot turn a gate off by editing the file next
// to the one the gate stopped it editing. Without a remote default it is the
// checkout's. It lives here because every reader of the shared log (the
// context() writer, the edit gate, the verify gate) must agree on it.
func LocalConfig(nugitRoot string) (config.Config, error) {
	repo := gitutil.Repo{Dir: nugitRoot}
	return config.LoadLocal(nugitRoot, func() (string, bool) {
		return repo.DefaultBranchFile(".nugit/config.yml", TargetReadTimeout)
	})
}

// Source values.
const (
	SourceCLI  = "cli"
	SourceMCP  = "mcp"
	SourceHook = "hook"
	// SourceBoundary marks the moment a session's context window lost what
	// earlier context() calls returned: a compaction or a /clear (ADR-0054).
	// It is neither a call nor an edit; it ends the cover of every call
	// before it for that session.
	SourceBoundary = "boundary"
	// SourceEvidence is a verification that ran in a session: the repo's
	// verify skill, a review, or the test suite (ADR-0056).
	SourceEvidence = "evidence"
	// SourceCommit is the commit gate's verdict on one commit (ADR-0056).
	SourceCommit = "commit"
)

// EndsContext reports whether a Claude Code SessionStart source discards the
// conversation the model was holding (ADR-0054). A compaction summarizes it
// and a /clear drops it; resume restores it, and a fresh startup has none to
// lose.
func EndsContext(sessionStartSource string) bool {
	return sessionStartSource == "compact" || sessionStartSource == "clear"
}

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
	// Verify is what the verify gate saw (ADR-0056); zero until it runs.
	Verify VerifyStats `json:"verify"`
}

// VerifyStats is the commit gate's record: the evidence sessions produced and
// what the gate said about each commit. WouldBlock is observe mode's point:
// the commits enforce would have stopped, to review for false positives
// before turning it on.
type VerifyStats struct {
	Evidence   map[string]int `json:"evidence,omitempty"` // by kind
	Commits    int            `json:"commits"`
	Verified   int            `json:"verified"`
	Explained  int            `json:"explained"`
	WouldBlock int            `json:"would_block"`
	Blocked    int            `json:"blocked"`
	Released   int            `json:"released"`
}

func (v *VerifyStats) add(r Record) {
	if r.Source == SourceEvidence {
		if v.Evidence == nil {
			v.Evidence = map[string]int{}
		}
		v.Evidence[r.Evidence]++
		return
	}
	switch r.Verdict {
	case VerdictBlock:
		v.Blocked++
		return // a blocked commit was not made
	case VerdictVerified:
		v.Verified++
	case VerdictExplained:
		v.Explained++
	case VerdictWouldBlock:
		v.WouldBlock++
	case VerdictRelease:
		v.Released++
	}
	v.Commits++
}

// Commit-gate verdicts (ADR-0056), one per commit record.
const (
	VerdictVerified   = "verified"    // session evidence backs the commit
	VerdictExplained  = "explained"   // the message says why it was not verified
	VerdictWouldBlock = "would-block" // observe: enforce would have stopped it
	VerdictBlock      = "block"       // enforce stopped it
	VerdictRelease    = "release"     // enforce let it through after two blocks
)

// EditStats measures edit-time coverage: how many edits the pre-edit hook saw
// and how many a context() call for that path preceded.
type EditStats struct {
	Seen        int            `json:"seen"`
	Covered     int            `json:"covered"`
	CoveragePct float64        `json:"coverage_pct"`
	ByCoveredBy map[string]int `json:"by_covered_by,omitempty"` // session | agent_session | branch
	// Resets counts the context boundaries recorded (ADR-0054): the times a
	// session's window was compacted or cleared, after which its earlier
	// context() calls no longer cover its edits.
	Resets int `json:"resets,omitempty"`
	// LostToReset counts uncovered edits that an earlier call would have
	// covered but for a reset: what the boundary rule changed.
	LostToReset int `json:"lost_to_reset,omitempty"`
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
		if r.Source == SourceBoundary {
			s.Edits.Resets++ // not a call: it must not count as context served
			continue
		}
		if r.Source == SourceEvidence || r.Source == SourceCommit {
			s.Verify.add(r)
			continue
		}
		if r.Source == SourceHook {
			ss := session(r)
			s.Edits.Seen++
			ss.Edits++
			if r.LostTo != "" {
				s.Edits.LostToReset++
			}
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
		s.writeVerify(&b)
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
	s.writeVerify(&b)
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
	if e.Resets > 0 {
		fmt.Fprintf(b, "- **context resets** (compact or /clear; earlier calls stop covering): %d\n", e.Resets)
		fmt.Fprintf(b, "  - edits uncovered only because of a reset: %d\n", e.LostToReset)
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

// writeVerify renders the commit gate's record (ADR-0056). would-block is the
// number to read before moving a repo from observe to enforce.
func (s Stats) writeVerify(b *strings.Builder) {
	v := s.Verify
	if v.Commits == 0 && v.Blocked == 0 && len(v.Evidence) == 0 {
		return
	}
	b.WriteString("\n### verification (verify gate)\n\n")
	if len(v.Evidence) > 0 {
		b.WriteString("- **evidence recorded**:")
		for _, k := range sortedKeys(v.Evidence) {
			fmt.Fprintf(b, " %s %d", k, v.Evidence[k])
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "- **agent commits**: %d — verified %d, explained %d, released %d\n", v.Commits, v.Verified, v.Explained, v.Released)
	fmt.Fprintf(b, "- **would have been blocked** (observe): %d\n", v.WouldBlock)
	fmt.Fprintf(b, "- **blocked** (enforce): %d\n", v.Blocked)
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
