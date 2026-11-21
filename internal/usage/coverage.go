package usage

import (
	"path"
	"strings"
	"time"
)

// DefaultCoverWindow bounds the branch fallback: with no session in common, a
// context() call on the same branch counts as covering an edit only if it was
// served this recently. Long enough for one working stretch on a change, short
// enough that yesterday's call on a long-lived branch does not cover today's
// edit (ADR-0042).
const DefaultCoverWindow = 4 * time.Hour

// Edit is one file edit the pre-edit hook saw, already resolved to repo
// coordinates.
type Edit struct {
	Time         time.Time
	Session      string
	AgentSession string
	Branch       string
	Path         string // git-root-relative, slash-separated
	AbsPath      string // absolute, symlinks resolved (matches an absolute context() path)
	Component    string // "" = unmapped
}

// FindCover returns the attribution that ties some earlier context() call in
// records to e ("session", "agent_session", or "branch"), or "" when none
// covers it. A call covers an edit when it is about the same component or a
// path containing the edited file, AND it belongs to the same Claude Code
// session, the same agent-deck session, or — the fallback, for calls that
// carry no matching session — the same branch within window. Session matches
// are preferred over a branch match so the record says how strong the
// evidence was. Hook records never cover anything: an edit is not a read.
//
// A context boundary ends cover (ADR-0054): once e's session was compacted or
// cleared, only calls made after its latest boundary can cover e, whichever
// attribution they would match by. The bundle an earlier call returned is no
// longer in the window, so it no longer informs the edit.
func FindCover(records []Record, e Edit, window time.Duration) string {
	return findCover(records, e, window, lastBoundary(records, e).Time)
}

// LostCover returns the boundary ("compact" or "clear") that ended the cover
// of a call which would otherwise have covered e, or "" when e is covered or
// was never covered (ADR-0054). It is what tells an agent why a context()
// call it remembers making no longer counts.
func LostCover(records []Record, e Edit, window time.Duration) string {
	b := lastBoundary(records, e)
	if b.Time.IsZero() || findCover(records, e, window, b.Time) != "" {
		return ""
	}
	if findCover(records, e, window, time.Time{}) == "" {
		return ""
	}
	return b.Boundary
}

// lastBoundary is the latest boundary record at or before e that belongs to
// e's session, by Claude Code session or agent-deck session (zero Record =
// none). The agent-deck id matters for /clear, which gives the conversation a
// new Claude Code session id while the MCP server keeps reporting the old one.
func lastBoundary(records []Record, e Edit) Record {
	var last Record
	for _, r := range records {
		if r.Source != SourceBoundary || r.Time.After(e.Time) {
			continue
		}
		mine := (e.Session != "" && r.Session == e.Session) ||
			(e.AgentSession != "" && r.AgentSession == e.AgentSession)
		if mine && r.Time.After(last.Time) {
			last = r
		}
	}
	return last
}

// findCover is FindCover with the boundary given: records at or before since
// cover nothing (zero since = no boundary).
func findCover(records []Record, e Edit, window time.Duration, since time.Time) string {
	best := ""
	for _, r := range records {
		if !r.IsCall() || r.Time.After(e.Time) || !scopeCovers(r, e) {
			continue
		}
		if !since.IsZero() && !r.Time.After(since) {
			continue
		}
		switch {
		case e.Session != "" && r.Session == e.Session:
			return "session"
		case e.AgentSession != "" && r.AgentSession == e.AgentSession:
			best = "agent_session"
		case best == "" && e.Branch != "" && e.Branch != "HEAD" && r.Branch == e.Branch &&
			e.Time.Sub(r.Time) <= window:
			best = "branch"
		}
	}
	return best
}

// scopeCovers reports whether context() call r was about the edited file:
// same resolved component, or a requested path that is the file or one of its
// ancestor directories. r.Path is whatever the agent passed — repo-relative or
// absolute — so it is compared against the matching form of the edit.
func scopeCovers(r Record, e Edit) bool {
	if r.Component != "" && r.Component == e.Component {
		return true
	}
	p := strings.TrimSpace(r.Path)
	if p == "" {
		return false
	}
	target := e.Path
	if strings.HasPrefix(p, "/") {
		target = e.AbsPath
	} else {
		p = strings.TrimPrefix(p, "./")
	}
	p = path.Clean(p)
	if p == "." {
		return true // the repo root covers everything in it
	}
	return target != "" && (target == p || strings.HasPrefix(target, p+"/"))
}
