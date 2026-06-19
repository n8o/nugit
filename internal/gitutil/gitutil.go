// Package gitutil wraps the handful of git plumbing calls the keystone needs.
// It shells out to the git binary (no cgo, no libgit2) to stay dependency-light.
package gitutil

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/n8o/nugit/internal/model"
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

// Prefix returns the path of Dir relative to the git toplevel, slash-terminated
// (e.g. "apps/operator/"), or "" when Dir IS the git root (the common case) or
// is not inside a git repo. This is the single bridge that lets a nugit root
// nested inside a larger repo speak git's git-root-relative path coordinates.
func (r Repo) Prefix() string {
	out, err := r.git("rev-parse", "--show-prefix")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Toplevel returns the absolute path of the git working-tree root, or Dir if it
// cannot be determined.
func (r Repo) Toplevel() string {
	out, err := r.git("rev-parse", "--show-toplevel")
	if err != nil {
		return r.Dir
	}
	return strings.TrimSpace(out)
}

// ShowFile returns the contents of path at ref. A genuinely-absent path yields
// ("", nil) so callers treat "added" and "removed" symmetrically — but any OTHER
// error (e.g. a bad ref) is returned, never masked as an empty file.
func (r Repo) ShowFile(ref, path string) (string, error) {
	out, err := r.git("show", ref+":"+path)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "does not exist in") || strings.Contains(msg, "exists on disk, but not in") {
			return "", nil
		}
		return "", err
	}
	return out, nil
}

// splitNUL splits NUL-delimited git output, dropping the trailing empty token.
func splitNUL(s string) []string {
	parts := strings.Split(s, "\x00")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// NameStatus returns the per-file change status between base and head. It uses
// -z (NUL-delimited, unquoted) so paths with spaces/non-ASCII and renames parse
// unambiguously; core.quotepath=false keeps non-ASCII bytes raw.
func (r Repo) NameStatus(base, head string) ([]model.FileChange, error) {
	out, err := r.git("-c", "core.quotepath=false", "diff", "--name-status", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	toks := splitNUL(out)
	var changes []model.FileChange
	for i := 0; i < len(toks); {
		status := toks[i]
		i++
		if status == "" {
			continue
		}
		var path string
		if status[0] == 'R' || status[0] == 'C' { // rename/copy: old, new
			if i+1 >= len(toks) {
				break
			}
			path = toks[i+1] // new path
			i += 2
		} else {
			if i >= len(toks) {
				break
			}
			path = toks[i]
			i++
		}
		changes = append(changes, model.FileChange{Path: path, Status: status[:1]})
	}
	return changes, nil
}

// Numstat fills in added/deleted line counts keyed by path (the new path for
// renames, matching NameStatus). Uses -z so the tab-delimited counts and the
// path never collide on spaces.
func (r Repo) Numstat(base, head string) (map[string][2]int, error) {
	out, err := r.git("-c", "core.quotepath=false", "diff", "--numstat", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	counts := map[string][2]int{}
	toks := splitNUL(out)
	for i := 0; i < len(toks); {
		rec := toks[i]
		i++
		if rec == "" {
			continue
		}
		// rec = "add\tdel\t<path?>"; path is empty for renames.
		fields := strings.SplitN(rec, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		add, _ := strconv.Atoi(fields[0]) // "-" (binary) parses to 0
		del, _ := strconv.Atoi(fields[1])
		path := fields[2]
		if path == "" { // rename: next two tokens are old, new
			if i+1 >= len(toks) {
				break
			}
			path = toks[i+1] // new path
			i += 2
		}
		counts[path] = [2]int{add, del}
	}
	return counts, nil
}

// ListTree returns every file path tracked at ref (git-root-relative), so a
// language analyzer can read build files at the reviewed ref instead of the disk
// working tree.
func (r Repo) ListTree(ref string) ([]string, error) {
	out, err := r.git("ls-tree", "-r", "--name-only", "-z", ref)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
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
