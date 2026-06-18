// Package gitutil wraps the handful of git plumbing calls the keystone needs.
// It shells out to the git binary (no cgo, no libgit2) to stay dependency-light.
package gitutil

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/burrowfarm/nugit/internal/model"
)

// Repo is a handle to a git working tree.
type Repo struct {
	Dir string
}

func (r Repo) git(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// MergeBase returns the best common ancestor of base and head. If it cannot be
// computed (e.g. unrelated refs), it falls back to base.
func (r Repo) MergeBase(base, head string) string {
	out, err := r.git("merge-base", base, head)
	if err != nil {
		return base
	}
	return strings.TrimSpace(out)
}

// ShowFile returns the contents of path at ref. A missing path yields ("", nil)
// so callers can treat "added" and "removed" symmetrically.
func (r Repo) ShowFile(ref, path string) (string, error) {
	out, err := r.git("show", ref+":"+path)
	if err != nil {
		// git exits non-zero when the path does not exist at that ref.
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not") || strings.Contains(err.Error(), "fatal: path") {
			return "", nil
		}
		// Treat any "not in" / unknown-path error as absent rather than fatal.
		return "", nil
	}
	return out, nil
}

// NameStatus returns the per-file change status between base and head.
func (r Repo) NameStatus(base, head string) ([]model.FileChange, error) {
	out, err := r.git("diff", "--name-status", "-M", base, head)
	if err != nil {
		return nil, err
	}
	var changes []model.FileChange
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		status := fields[0]
		path := fields[len(fields)-1] // for renames, the new path is last
		changes = append(changes, model.FileChange{Path: path, Status: status[:1]})
	}
	return changes, nil
}

// Numstat fills in added/deleted line counts keyed by path.
func (r Repo) Numstat(base, head string) (map[string][2]int, error) {
	out, err := r.git("diff", "--numstat", "-M", base, head)
	if err != nil {
		return nil, err
	}
	counts := map[string][2]int{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		add, _ := strconv.Atoi(fields[0]) // "-" (binary) parses to 0
		del, _ := strconv.Atoi(fields[1])
		path := fields[len(fields)-1]
		counts[path] = [2]int{add, del}
	}
	return counts, nil
}

// RawDiff returns the unified text diff between base and head, optionally
// limited to the given paths.
func (r Repo) RawDiff(base, head string, paths ...string) (string, error) {
	args := []string{"diff", base, head}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return r.git(args...)
}

// commitSep / fieldSep are unlikely byte sequences used to make `git log`
// output unambiguously machine-parseable.
const (
	commitSep = "\x1e" // record separator
	fieldSep  = "\x1f" // unit separator
)

// Log returns the commits in (base, head], newest last, with full bodies.
func (r Repo) Log(base, head string) ([]model.Commit, error) {
	format := strings.Join([]string{"%H", "%s", "%b"}, fieldSep) + commitSep
	out, err := r.git("log", "--reverse", "--pretty=format:"+format, base+".."+head)
	if err != nil {
		return nil, err
	}
	var commits []model.Commit
	for _, rec := range strings.Split(out, commitSep) {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, fieldSep, 3)
		c := model.Commit{SHA: parts[0]}
		if len(parts) > 1 {
			c.Subject = parts[1]
		}
		if len(parts) > 2 {
			c.Body = parts[2]
		}
		commits = append(commits, c)
	}
	return commits, nil
}
