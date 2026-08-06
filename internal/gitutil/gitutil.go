// Package gitutil wraps the handful of git plumbing calls the keystone needs.
// It shells out to the git binary (no cgo, no libgit2) to stay dependency-light.
package gitutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
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

// HeadSHA returns the full sha HEAD resolves to, or "" when it cannot be (not a
// git repo, an unborn branch). Callers that stamp provenance treat "" as "no
// commit recorded" rather than failing: a record is still promotable out of a
// checkout that has no commits yet, it just cannot say which one it came from.
func (r Repo) HeadSHA() string {
	out, err := r.git("rev-parse", "HEAD")
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

// HooksDir returns the absolute path of the hooks directory git will actually
// run for this repo (`rev-parse --git-path hooks`, which honours
// core.hooksPath), or "" when Dir is not in a git repo.
//
// It answers "where does git look?", NOT "where does a developer commit a
// hook?". Under a shim-generating hook manager those are two different
// directories; CommitMsgHook is what reconciles them.
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

// defaultHooksDir returns $GIT_COMMON_DIR/hooks — git's built-in hooks
// location, the one core.hooksPath masks — or "" outside a git repo.
func (r Repo) defaultHooksDir() string {
	gd := r.CommonGitDir()
	if gd == "" {
		return ""
	}
	return filepath.Join(gd, "hooks")
}

// nugitHookSignature is the command every hook `nugit init` writes carries. It
// is what makes a commit-msg hook nugit's, and matching on it (rather than on
// the generated header comment) keeps working when a repo chains nugit into a
// hook of its own — the supported way to coexist with another validator.
const nugitHookSignature = "nugit hook commit-msg"

// isNugitHook reports whether the file at path delegates to nugit. An unreadable
// file is "not nugit's": detection degrades to absent, never to a crash.
func isNugitHook(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(b), nugitHookSignature)
}

// hookShimParent reports the developer-owned hooks directory that a GENERATED
// shim directory delegates to (its parent), or "" when dir is not one.
//
// The rule is structural on purpose, not a match on the literal name `.husky`.
// A shim-generating hook manager — husky >= 9, which most of the JavaScript
// ecosystem uses — points core.hooksPath at a `_` subdirectory of a directory
// it does not own, regenerates that `_` on every package install, gitignores
// it, and has each generated shim run the same-named file in the PARENT: the
// file a developer actually writes and commits. Two things are observable from
// the path alone, which is what matters here, because the shim directory does
// not exist in a fresh checkout until the package manager runs — precisely the
// state in which this check used to report a correctly-installed hook missing:
//
//   - the base name is exactly "_", the conventional "generated, do not edit"
//     marker; and
//   - the parent is an existing directory strictly inside the working tree,
//     i.e. somewhere a developer can commit a file to.
//
// The parent's NAME is the half of the convention that varies (husky takes the
// directory as an argument; `.husky` is only its default), so keying on it
// would be the brittle choice. The `_` child is the invariant. "Strictly
// inside" excludes the working-tree root itself, so a core.hooksPath of `_` at
// the top level cannot turn the whole checkout into a hooks directory.
func hookShimParent(dir, worktreeRoot string) string {
	if dir == "" || worktreeRoot == "" || filepath.Base(dir) != "_" {
		return ""
	}
	parent := filepath.Dir(dir)
	if !strictlyWithin(parent, worktreeRoot) {
		return ""
	}
	if fi, err := os.Stat(parent); err != nil || !fi.IsDir() {
		return ""
	}
	return parent
}

// resolvePath normalizes symlinked spellings (macOS /var -> /private/var) so
// paths reached by different routes compare equal, degrading to Clean when the
// path does not exist.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// strictlyWithin reports whether p is a proper descendant of root — never root
// itself.
func strictlyWithin(p, root string) bool {
	rel, err := filepath.Rel(resolvePath(root), resolvePath(p))
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..")
}

// sameDirAny reports whether dir names the same directory as any of dirs.
func sameDirAny(dir string, dirs []string) bool {
	rd := resolvePath(dir)
	for _, d := range dirs {
		if resolvePath(d) == rd {
			return true
		}
	}
	return false
}

// CommitMsgHook is where a repo's commit-msg hook stands. It is the ONE search
// order that `nugit doctor` (detection) and `nugit init` (installation) both
// read, so the two cannot disagree about a repo: Target is by construction the
// last of Dirs, and Dirs is exactly what detection scans.
type CommitMsgHook struct {
	// GitHooksDir is the directory git runs (HooksDir): core.hooksPath when set,
	// git's default hooks dir otherwise. "" outside a git repo.
	GitHooksDir string
	// Dirs are the directories whose commit-msg git will execute, in precedence
	// order: GitHooksDir first, then — when GitHooksDir is a generated shim dir
	// — the developer-owned parent its shims run.
	Dirs []string
	// Path is the commit-msg hook in Dirs that delegates to nugit, or "" when
	// none does.
	Path string
	// Target is where `nugit init` writes: the developer-owned end of the chain,
	// never the generated shim dir (which the hook manager rewrites and
	// gitignores). "" outside a git repo.
	Target string
	// Foreign are commit-msg hooks present in Dirs that do NOT delegate to nugit
	// — a commitlint hook, or a hook manager's own generated shim. `init` never
	// overwrites one.
	Foreign []string
	// Inert is a nugit commit-msg hook sitting in git's DEFAULT hooks directory
	// while core.hooksPath sends git somewhere else: installed, but dead. Empty
	// in the ordinary case, where the default IS the directory git runs.
	Inert string
}

// Installed reports whether a nugit commit-msg hook exists somewhere git runs.
func (h CommitMsgHook) Installed() bool { return h.Path != "" }

// Shimmed reports whether the directory git runs is a generated shim dir, so
// the hook a developer commits lives one level up.
func (h CommitMsgHook) Shimmed() bool { return len(h.Dirs) > 1 }

// ForeignAtTarget returns the path of a non-nugit commit-msg hook occupying the
// install target, or "".
func (h CommitMsgHook) ForeignAtTarget() string {
	for _, p := range h.Foreign {
		if p == h.Target {
			return p
		}
	}
	return ""
}

// CommitMsgHook resolves where this repo's commit-msg hook lives, and where one
// belongs. Every git failure degrades to the zero value — nothing found,
// nothing to install — so doctor reports "not installed" and init returns
// quietly, exactly as they did before core.hooksPath was understood. It never
// crashes and never blocks.
func (r Repo) CommitMsgHook() CommitMsgHook {
	h := CommitMsgHook{GitHooksDir: r.HooksDir()}
	if h.GitHooksDir == "" {
		return h
	}
	h.Dirs = []string{h.GitHooksDir}
	if root, err := r.WorktreeRoot(); err == nil {
		if parent := hookShimParent(h.GitHooksDir, root); parent != "" {
			h.Dirs = append(h.Dirs, parent)
		}
	}
	h.Target = filepath.Join(h.Dirs[len(h.Dirs)-1], "commit-msg")
	for _, d := range h.Dirs {
		p := filepath.Join(d, "commit-msg")
		if _, err := os.Stat(p); err != nil {
			continue // a hooks dir that does not exist yet is simply empty
		}
		switch {
		case !isNugitHook(p):
			h.Foreign = append(h.Foreign, p)
		case h.Path == "":
			h.Path = p
		}
	}
	// The plain fallback: git's built-in hooks dir. With core.hooksPath unset it
	// IS GitHooksDir and this adds nothing. With core.hooksPath set, a nugit hook
	// left behind there is installed but unreachable — worth naming, because
	// "your hook is in the wrong place" is a far better message than either
	// silence or a green check git would not honour.
	if def := r.defaultHooksDir(); def != "" && !sameDirAny(def, h.Dirs) {
		p := filepath.Join(def, "commit-msg")
		if _, err := os.Stat(p); err == nil && isNugitHook(p) {
			h.Inert = p
		}
	}
	return h
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

// TrackedFiles returns the set of paths git tracks under Dir, Dir-relative and
// slash-separated (`git ls-files` reports relative to the -C directory, the
// same coordinates the filesystem detectors speak). -z keeps paths with spaces
// or non-ASCII bytes unambiguous, matching NameStatus.
//
// It is ONE call for the whole subtree on purpose: callers ask it about
// thousands of candidate files, so a per-candidate `git ls-files <path>` is not
// an option. An error means "no tracking information here" (not a git repo, no
// git binary, a bare repo) — callers must degrade to their non-git behaviour
// rather than read it as "nothing is tracked".
func (r Repo) TrackedFiles() (map[string]bool, error) {
	out, err := r.git("-c", "core.quotepath=false", "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	paths := splitNUL(out)
	tracked := make(map[string]bool, len(paths))
	for _, p := range paths {
		if p != "" {
			tracked[p] = true
		}
	}
	return tracked, nil
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

// LastCommitFor returns the newest commit touching path (repo-relative) as
// (sha, committer date in RFC3339). A path no commit has ever touched — an
// untracked or brand-new file — yields ("", "", nil) rather than an error, so a
// caller reporting document staleness can say "never committed" instead of
// failing the whole report.
func (r Repo) LastCommitFor(path string) (sha, date string, err error) {
	out, err := r.git("log", "-1", "--format=%H"+fieldSep+"%cI", "--", path)
	if err != nil {
		return "", "", err
	}
	s := strings.TrimSpace(out)
	if s == "" {
		return "", "", nil
	}
	parts := strings.SplitN(s, fieldSep, 2)
	if len(parts) < 2 {
		return parts[0], "", nil
	}
	return parts[0], parts[1], nil
}

// CountCommits returns how many commits revRange contains ("HEAD", or
// "<sha>..HEAD"), bounded by max: the walk stops there and capped reports that
// the true number is at least n. The bound exists so a staleness scan over a
// long history stays O(max) per document rather than O(history).
func (r Repo) CountCommits(revRange string, max int) (n int, capped bool, err error) {
	args := []string{"rev-list", "--count"}
	if max > 0 {
		args = append(args, "--max-count="+strconv.Itoa(max))
	}
	args = append(args, revRange)
	out, err := r.git(args...)
	if err != nil {
		return 0, false, err
	}
	n, err = strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, false, err
	}
	return n, max > 0 && n >= max, nil
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
