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
func FindCover(records []Record, e Edit, window time.Duration) string {
	best := ""
	for _, r := range records {
		if r.Source == SourceHook || r.Time.After(e.Time) || !scopeCovers(r, e) {
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
