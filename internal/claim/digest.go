package claim

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/gitutil"
)

// MaxDigest caps the SessionStart digest. Claude Code swaps a long
// additionalContext for a short preview, and this text is read every session,
// so it stays a few short paragraphs.
const MaxDigest = 2000

// maxListed is how many in-progress beads the unclaimed digest names.
const maxListed = 5

// Digest is the SessionStart text for the session in repoDir (a nugit root),
// or "" when the repo keeps no .nugit/ store.
func Digest(repoDir string) string {
	if fi, err := os.Stat(filepath.Join(repoDir, ".nugit")); err != nil || !fi.IsDir() {
		return ""
	}
	st, _ := beads.ReadDir(repoDir)
	text := digest(Resolve(repoDir), st, defaultShort(repoDir))
	if len(text) > MaxDigest {
		text = strings.ToValidUTF8(text[:MaxDigest-len("…")], "") + "…"
	}
	return text
}

func defaultShort(repoDir string) string {
	ref := gitutil.Repo{Dir: repoDir}.DefaultRemoteBranch()
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

const rule = "nugit (ADR-0048): one session works toward one objective, a bead in the plan store. " +
	"The PR view opens with it, so the reviewer sees what the change is for."

func digest(cur Current, st beads.Store, defaultBranch string) string {
	var b strings.Builder
	b.WriteString(rule + "\n\n")
	switch {
	case cur.Branch == "":
		b.WriteString("HEAD is detached, so no objective can be claimed. Create a branch for the task before changing code.\n")
		return b.String()
	case cur.Objective != "":
		bead := Lookup(st, cur.Objective)
		if bead.Found {
			fmt.Fprintf(&b, "This session's objective: %s — %s (status: %s).\n", bead.ID, bead.Title, bead.Status)
		} else {
			fmt.Fprintf(&b, "This session's objective: %s. It is not in the plan store (.beads/) yet: create or export it in this PR so the PR view can show it.\n", bead.ID)
		}
		if cur.Source == FromCommits {
			fmt.Fprintf(&b, "It is named by this branch's commits but not claimed: run `nugit objective claim %s` so the commit-msg hook names it on every commit.\n", bead.ID)
		} else {
			fmt.Fprintf(&b, "The commit-msg hook adds `objective: %s` to each commit on %s.\n", bead.ID, cur.Branch)
		}
		b.WriteString("Stay on this objective. For unrelated work, tell the user and start a separate session on its own branch instead of adding it to this PR. " +
			"When the objective is done, close its bead in the same PR.\n")
		return b.String()
	}
	if defaultBranch != "" && cur.Branch == defaultBranch {
		fmt.Fprintf(&b, "You are on %s. Create a branch for the task before changing code, then claim its objective.\n", cur.Branch)
	} else {
		fmt.Fprintf(&b, "No objective is claimed for branch %s. Before changing code:\n", cur.Branch)
		b.WriteString("1. Find the bead for this task, or create one with acceptance criteria.\n")
		b.WriteString("2. Run `nugit objective claim <bead-id>`. The commit-msg hook then names it on every commit.\n")
	}
	var active []string
	open := 0
	for _, it := range st.Issues {
		switch {
		case beads.IsActive(it.Status):
			active = append(active, it.ID+" — "+it.Title)
		case !beads.IsDone(it.Status):
			open++
		}
	}
	if len(active) > 0 {
		b.WriteString("\nIn progress in the plan store:\n")
		for i, a := range active {
			if i == maxListed {
				fmt.Fprintf(&b, "- +%d more\n", len(active)-maxListed)
				break
			}
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	if open > 0 {
		fmt.Fprintf(&b, "%d other bead(s) are open in .beads/.\n", open)
	}
	return b.String()
}
