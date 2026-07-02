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
	"github.com/n8o/nugit/internal/retrieval"
)

// LogPath is where records land, relative to the repo dir. It lives under
// .nugit/.cache/ — the designated rebuildable, gitignored local dir; the
// canonical store stays git text only.
const LogPath = ".nugit/.cache/usage.jsonl"

// Record is one served context() call.
type Record struct {
	Time            time.Time `json:"time"`
	Source          string    `json:"source"` // cli | mcp
	Path            string    `json:"path"`
	Task            string    `json:"task,omitempty"`
	Component       string    `json:"component"` // "" = path resolved to no component
	BudgetTokens    int       `json:"budget_tokens"`
	EstimatedTokens int       `json:"estimated_tokens"`
	Truncated       bool      `json:"truncated"`
	Decisions       int       `json:"decisions"`
	Lessons         int       `json:"lessons"`
	Glossary        int       `json:"glossary"`
	WorkingMemory   int       `json:"working_memory"`
	Spec            bool      `json:"spec"`
	Dropped         int       `json:"dropped"`
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
		Glossary:        len(b.Glossary),
		WorkingMemory:   len(b.WorkingMemory),
		Spec:            b.Spec != nil,
		Dropped:         len(b.Dropped),
	}
}

// Log records a served bundle unless config.yml says `usage: {log: off}`.
// Callers deliberately ignore the returned error on the serving path.
func Log(repoDir, source, task string, b retrieval.Bundle) error {
	cfg, _ := config.Load(repoDir)
	if cfg.Usage.Log == "off" {
		return nil
	}
	return Append(repoDir, FromBundle(source, task, b))
}

// Append writes one record to the log, creating .nugit/.cache/ if needed.
func Append(repoDir string, r Record) error {
	dir := filepath.Join(repoDir, ".nugit", ".cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(repoDir, LogPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
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

// Read loads all records. A missing log yields (nil, nil); malformed lines are
// skipped (a half-written line from a crashed process must not poison stats).
func Read(repoDir string) ([]Record, error) {
	f, err := os.Open(filepath.Join(repoDir, LogPath))
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
	Unresolved   int            `json:"unresolved"`    // no component matched: model coverage gap
	EmptyBundles int            `json:"empty_bundles"` // no decisions/spec/lessons matched: capture gap
	Truncated    int            `json:"truncated"`
	AvgTokens    int            `json:"avg_estimated_tokens"`
}

// Aggregate computes Stats over records at or after since (zero = all).
func Aggregate(records []Record, since time.Time) Stats {
	s := Stats{BySource: map[string]int{}, ByComponent: map[string]int{}}
	tokens := 0
	for _, r := range records {
		if !since.IsZero() && r.Time.Before(since) {
			continue
		}
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
		type kv struct {
			k string
			v int
		}
		var top []kv
		for k, v := range s.ByComponent {
			top = append(top, kv{k, v})
		}
		sort.Slice(top, func(i, j int) bool {
			if top[i].v != top[j].v {
				return top[i].v > top[j].v
			}
			return top[i].k < top[j].k
		})
		if len(top) > 10 {
			top = top[:10]
		}
		for _, t := range top {
			fmt.Fprintf(&b, "  - %s: %d\n", t.k, t.v)
		}
	}
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
