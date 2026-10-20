package orphans

import (
	"fmt"
	"strings"
)

// heading and why describe each section, so the report reads without the ADR.
var (
	heading = map[Kind]string{
		StalledObjective: "Stalled objectives",
		StoppedSession:   "Stopped sessions with an open objective",
		ClosedUnmerged:   "Closed objectives with work left on a branch",
		StrayBranch:      "Branches with unmerged work",
		StaleNote:        "Working notes never recorded",
		MergedBranch:     "Merged branches left behind",
	}
	why = map[Kind]string{
		StalledObjective: "Started (in progress, claimed, or named by commits) and quiet since.",
		StoppedSession:   "The session that claimed it is not running, and the bead is still open.",
		ClosedUnmerged:   "The bead is closed, but a branch for it holds commits the default branch lacks.",
		StrayBranch:      "Commits that are not on the default branch, with nothing recent on them.",
		StaleNote:        "`nugit remember` notes that never became a decision or lesson.",
		MergedBranch:     "Everything on them is already merged (often by squash); only cleanup is left.",
	}
)

// maxNamed caps the merged branches named in the markdown summary.
const maxNamed = 10

// Markdown renders the report for a reviewer: one section per kind, each
// orphan with the decision to make.
func Markdown(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## nugit orphans — %d to decide\n\n", decisions(r))
	fmt.Fprintf(&b, "_Against `%s`; quiet for %s or more counts as orphaned._", r.Default, r.Stale)
	if !r.Sessions {
		b.WriteString(" _agent-deck was not consulted, so stopped sessions are not listed._")
	}
	if !r.GitHub {
		b.WriteString(" _GitHub was not consulted, so a squash-merged branch can show as unmerged._")
	}
	b.WriteString("\n")
	if len(r.Orphans) == 0 {
		b.WriteString("\nNothing is orphaned.\n")
		return b.String()
	}
	for _, k := range kindOrder {
		var os []Orphan
		for _, o := range r.Orphans {
			if o.Kind == k {
				os = append(os, o)
			}
		}
		if len(os) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s (%d)\n\n_%s_\n\n", heading[k], len(os), why[k])
		if k == MergedBranch {
			// Cleanup, not a decision: one line, not one entry per branch.
			names := make([]string, len(os))
			for i, o := range os {
				names[i] = "`" + o.Subject + "`"
			}
			if len(names) > maxNamed {
				names = append(names[:maxNamed], fmt.Sprintf("+%d more", len(os)-maxNamed))
			}
			fmt.Fprintf(&b, "- %s\n  - ➜ delete them (and any worktree they are checked out in); `-format json` lists every one\n", strings.Join(names, ", "))
			continue
		}
		for _, o := range os {
			if o.Title != "" {
				fmt.Fprintf(&b, "- **%s** — %s · idle %s\n", o.Subject, oneLine(o.Title), days(o.Idle))
			} else {
				fmt.Fprintf(&b, "- **%s** · idle %s\n", o.Subject, days(o.Idle))
			}
			fmt.Fprintf(&b, "  - %s\n  - ➜ %s\n", o.Detail, o.Action)
		}
	}
	return b.String()
}

// decisions counts the orphans that need a finish-or-drop call; merged
// leftovers are only cleanup.
func decisions(r Report) int {
	n := 0
	for _, o := range r.Orphans {
		if o.Kind != MergedBranch {
			n++
		}
	}
	return n
}
