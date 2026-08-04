// Package gitutil wraps the handful of git plumbing calls the keystone needs.
// It shells out to the git binary (no cgo, no libgit2) to stay dependency-light.
package gitutil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

// CurrentBranch returns the branch HEAD points at (`git rev-parse
// --abbrev-ref HEAD`), trimmed. It returns "" on any error (e.g. not a git
// repo, or an unborn branch with no commits); a detached HEAD returns the
// literal string "HEAD" verbatim, exactly as git reports it.
func (r Repo) CurrentBranch() string {
	out, err := r.git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HooksDir returns the absolute path of the repo's git hooks directory, or ""
// when Dir is not in a git repo.
func (r Repo) HooksDir() string {
	out, err := r.git("rev-parse", "--git-path", "hooks")
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Dir, p)
	}
	return p
}

// Toplevel returns the absolute path of the git working-tree root, or Dir if it
// cannot be determined.
func (r Repo) Toplevel() string {
	top, err := r.WorktreeRoot()
	if err != nil {
		return r.Dir
	}
	return top
}

// WorktreeRoot returns the absolute root of the working tree containing Dir,
// or an error when Dir is not inside a git working tree — unlike Toplevel,
// which masks "not a repo" by returning Dir. Per-request MCP root resolution
// needs the failure visible so it can fall back safely (ADR-0025).
func (r Repo) WorktreeRoot() (string, error) {
	out, err := r.git("rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	top := strings.TrimSpace(out)
	if top == "" {
		return "", fmt.Errorf("git -C %s: no working tree (bare repo?)", r.Dir)
	}
	return top, nil
}

// CommonGitDir returns the absolute, symlink-resolved path of the repository's
// shared .git directory (`git rev-parse --git-common-dir`) — identical for
// every linked `git worktree` of one repository — or "" when Dir is not inside
// a git repo. It is the repo-identity key that per-request root resolution
// compares before honoring a client-supplied cwd (ADR-0025).
func (r Repo) CommonGitDir() string {
	out, err := r.git("rev-parse", "--git-common-dir")
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(out)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		base := r.Dir
		if abs, aerr := filepath.Abs(base); aerr == nil {
			base = abs
		}
		p = filepath.Join(base, p)
	}
	// Normalize symlinked spellings (e.g. macOS /var -> /private/var) so the
	// same repo reached via different paths compares equal.
	if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
		p = resolved
	}
	return filepath.Clean(p)
}

// CommonRoot returns the root of the MAIN working tree shared by every linked
// worktree of the repository — the parent of the common .git directory. From
// the main checkout it equals Toplevel(); from a linked `git worktree` it is
// the main checkout's root, NOT the worktree's. It falls back to Toplevel()
// when the common git dir is detached from any working tree (`git init
// --separate-git-dir`) and to Dir when not inside a git repo at all. This is
// where per-machine derived state (.nugit-local/, .nugit/.cache/) resolves so
// all worktrees of one repo share it (ADR-0025).
func (r Repo) CommonRoot() string {
	gd := r.CommonGitDir()
	if gd == "" {
		return r.Dir
	}
	if filepath.Base(gd) == ".git" {
		return filepath.Dir(gd)
	}
	return r.Toplevel()
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
	return parseNumstat(out), nil
}

// NumstatCached returns added/deleted line counts for the STAGED diff (index
// vs HEAD), keyed by path — what a commit-msg hook can classify, since it runs
// after staging. The git call is hard-bounded by timeout so a hook-path caller
// can never stall a commit; a timeout, an unborn HEAD, or any other git
// failure returns an error for the caller to degrade on (the capture nudge
// goes silent rather than block, ADR-0023).
func (r Repo) NumstatCached(timeout time.Duration) (map[string][2]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir,
		"-c", "core.quotepath=false", "diff", "--cached", "--numstat", "-z", "-M")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff --cached --numstat: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	return parseNumstat(out.String()), nil
}

// parseNumstat parses `git diff --numstat -z` output into add/del counts keyed
// by path (the new path for renames).
func parseNumstat(out string) map[string][2]int {
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
	return counts
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

// LogSince returns up to maxCount non-merge commits reachable from ref whose
// commit date falls within the last sinceDays days, optionally limited to
// paths matching the given glob patterns. Patterns are git-root-relative and
// passed with `:(top,glob)` pathspec magic, so the result is independent of
// the working directory (matching the model's git-root-relative path globs).
// Newest first, full bodies. Both bounds exist so a history scan (e.g. the
// recurrence check) stays O(window), never O(history).
func (r Repo) LogSince(ref string, sinceDays, maxCount int, globs ...string) ([]model.Commit, error) {
	args := []string{"log", "--no-merges",
		fmt.Sprintf("--since=%d.days", sinceDays),
		fmt.Sprintf("--max-count=%d", maxCount),
		"--pretty=format:" + logFormat, ref}
	if len(globs) > 0 {
		args = append(args, "--")
		for _, g := range globs {
			args = append(args, ":(top,glob)"+g)
		}
	}
	out, err := r.git(args...)
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

// logFormat is the machine-parseable pretty format shared by Log and LogPath.
const logFormat = "%H" + fieldSep + "%s" + fieldSep + "%b" + commitSep

// parseCommits parses `git log --pretty=format:logFormat` output into commits,
// in the order git emitted them.
func parseCommits(out string) []model.Commit {
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
	return commits
}

// Log returns the commits in (base, head], newest last, with full bodies.
func (r Repo) Log(base, head string) ([]model.Commit, error) {
	out, err := r.git("log", "--reverse", "--pretty=format:"+logFormat, base+".."+head)
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

// LogPath returns up to n commits touching path (newest first), restricted to
// the since window when non-empty (git approxidate, e.g. "90 days"). Bodies
// are full, so callers can read trailer lines. Bounded by construction (-n
// plus --since) so it stays cheap on long histories.
func (r Repo) LogPath(path string, n int, since string) ([]model.Commit, error) {
	args := []string{"log", "-n", strconv.Itoa(n), "--pretty=format:" + logFormat}
	if since != "" {
		args = append(args, "--since="+since)
	}
	args = append(args, "--", path)
	out, err := r.git(args...)
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}
