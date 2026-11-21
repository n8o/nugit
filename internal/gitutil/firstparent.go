package gitutil

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FirstParentChange is one commit on a ref's first-parent line, sized by its
// diff against its first parent, so a merge commit is the change it brought in.
type FirstParentChange struct {
	SHA     string
	Time    time.Time // committer time: when it landed on the line
	Subject string
	Body    string
	Files   []string
	Churn   int // added + deleted lines; a binary file adds none
}

// gitSince renders t for git's --since, in a form approxidate never misreads.
func gitSince(t time.Time) string { return "--since=" + t.UTC().Format("2006-01-02 15:04:05 +0000") }

// FirstParentLog returns ref's first-parent commits landed at or after since
// (zero: no bound), newest first, at most maxCount, each with the files and
// line churn of its diff against its first parent. Renames read as a delete
// and an add, and path quoting, colour and external diff drivers are pinned
// off, so the sizes do not depend on the clone's config.
func (r Repo) FirstParentLog(ref string, since time.Time, maxCount int) ([]FirstParentChange, error) {
	args := []string{"-c", "core.quotepath=false", "log", "--first-parent", "--diff-merges=first-parent",
		"--numstat", "--no-renames", "--no-color", "--no-ext-diff",
		"--format=" + commitSep + "%H" + fieldSep + "%ct" + fieldSep + "%s" + fieldSep + "%b" + fieldSep,
		fmt.Sprintf("--max-count=%d", maxCount)}
	if !since.IsZero() {
		args = append(args, gitSince(since))
	}
	out, err := r.git(append(args, ref, "--")...)
	if err != nil {
		return nil, err
	}
	var cs []FirstParentChange
	for _, rec := range strings.Split(out, commitSep) {
		f := strings.SplitN(rec, fieldSep, 5)
		if len(f) < 5 {
			continue
		}
		sec, _ := strconv.ParseInt(f[1], 10, 64)
		c := FirstParentChange{SHA: f[0], Time: time.Unix(sec, 0).UTC(), Subject: f[2], Body: strings.TrimSpace(f[3])}
		for _, line := range strings.Split(f[4], "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 3 {
				continue
			}
			a, _ := strconv.Atoi(parts[0]) // "-" for a binary file reads as 0
			d, _ := strconv.Atoi(parts[1])
			c.Churn += a + d
			c.Files = append(c.Files, parts[2])
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// RevertLog returns the non-merge commits reachable from ref, on any parent
// line, landed at or after since, whose message says "This reverts commit",
// newest first, at most maxCount. A revert merged in on a branch is found as
// well as one committed straight to the line.
func (r Repo) RevertLog(ref string, since time.Time, maxCount int) ([]TimedCommit, error) {
	return r.logTimed("--no-merges", "--fixed-strings", "--grep=This reverts commit",
		gitSince(since), fmt.Sprintf("--max-count=%d", maxCount), ref)
}

// FirstParentContaining returns the oldest first-parent commit of ref that
// descends from sha: the change that brought sha in, for a sha that is not on
// the first-parent line itself (one on it is its own change; the caller checks
// that first). It reads at most maxCount first-parent commits, newest first,
// and returns "" when that many do not reach back to it, or ref does not
// contain sha.
func (r Repo) FirstParentContaining(ref, sha string, maxCount int) string {
	out, err := r.git("rev-list", "--first-parent", "--ancestry-path",
		fmt.Sprintf("--max-count=%d", maxCount), sha+".."+ref)
	if err != nil {
		return ""
	}
	lines := strings.Fields(out)
	if len(lines) == 0 || len(lines) >= maxCount {
		return ""
	}
	return lines[len(lines)-1]
}
