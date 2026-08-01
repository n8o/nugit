package retrieval

import (
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/trailers"
)

// Path-history bounds (ADR-0024): enough commits to catch a recent fix on the
// queried path without ever dominating the bundle; the window keeps ancient
// history out.
const (
	historyCommits = 5
	historyWindow  = "90 days"
)

// HistoryEntry is one recent commit touching the queried path: the subject
// plus any decision:/learned: trailer lines. This is how an "orphaned trailer"
// — a captured why that never became a store object — surfaces at read time.
type HistoryEntry struct {
	SHA      string `json:"sha"` // short (12 hex) — a display coordinate, not a key
	Subject  string `json:"subject"`
	Decision string `json:"decision,omitempty"`
	Learned  string `json:"learned,omitempty"`
	tokens   int
}

// pathHistory derives "recent capture on these paths": a bounded
// `git log -- <path>` over the queried path, at read time. Zero store writes,
// no index — the same pure-projection posture as the rest of the bundle — and
// best-effort: not a git repo, an unborn branch, or git failing yields an
// empty section, never a failed bundle.
func pathHistory(repoDir, path string) []HistoryEntry {
	commits, err := gitutil.Repo{Dir: repoDir}.LogPath(path, historyCommits, historyWindow)
	if err != nil {
		return nil
	}
	var out []HistoryEntry
	for _, c := range commits {
		t := trailers.Parse(c.Body)
		e := HistoryEntry{SHA: shortSHA(c.SHA), Subject: c.Subject, Decision: t.Decision, Learned: t.Learned}
		e.tokens = tokensOf(e.SHA) + tokensOf(e.Subject) + tokensOf(e.Decision) + tokensOf(e.Learned) + 6
		out = append(out, e)
	}
	return out
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
