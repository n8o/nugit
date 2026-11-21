package claim

import (
	"fmt"
	"io"
	"os"
)

// StampFile is the commit-msg hook's objective step (ADR-0048, ADR-0049): it
// appends `objective: <id>` to the message file when the branch's objective
// comes from a claim or from the branch's name and the message names none,
// and says so on w. A message that names a different objective is left
// alone, with a warning. The branch's own commits never decide a stamp: on a
// stacked branch they include the base branch's. It never fails a commit.
func StampFile(repoDir, msgFile string, w io.Writer) {
	defer func() { _ = recover() }()
	cur := Resolve(repoDir)
	if cur.Source != FromClaim && cur.Source != FromBranchName {
		return
	}
	b, err := os.ReadFile(msgFile)
	if err != nil {
		return
	}
	out, added, other := Stamp(string(b), cur.Objective)
	switch {
	case other != "":
		fmt.Fprintf(w, "nugit: this commit names objective %s, but %s works toward %s; one session, one objective (ADR-0048)\n",
			other, cur.Branch, cur.Objective)
	case added:
		how := "claimed for"
		if cur.Source == FromBranchName {
			how = "named by branch"
		}
		if os.WriteFile(msgFile, []byte(out), 0o644) == nil {
			fmt.Fprintf(w, "nugit: added `objective: %s` (%s %s)\n", cur.Objective, how, cur.Branch)
		}
	}
}
