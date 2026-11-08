// Package claim records which objective a branch's session works toward
// (ADR-0048 phase 2) and builds the SessionStart digest that tells the agent.
//
// A claim is local session state, like the working-memory journal: one
// NDJSON line per claim in .nugit-local/claims.ndjson at the repo's common
// root, so every worktree of a repo shares one log (ADR-0025). Each claim is a
// single O_APPEND write, so concurrent agents never interleave; the newest
// line for a branch wins. The claim is how the commit-msg hook can name the
// objective on every commit without the agent remembering to.
package claim

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/trailers"
)

const (
	dir  = ".nugit-local"
	file = "claims.ndjson"
)

// Claim is one line of the log.
type Claim struct {
	Time      string `json:"time"`
	Branch    string `json:"branch"`
	Objective string `json:"objective"`
	// Session and AgentSession identify who claimed it (Claude Code's session
	// id and agent-deck's instance id), so a later orphan report can match a
	// stopped session to the objective it left open.
	Session      string `json:"session,omitempty"`
	AgentSession string `json:"agent_session,omitempty"`
}

// ErrOtherObjective is returned by Add when the branch already works toward a
// different objective: one session, one objective.
var ErrOtherObjective = errors.New("branch already claims another objective")

func logPath(repoDir string) string {
	return filepath.Join(gitutil.Repo{Dir: repoDir}.CommonRoot(), dir, file)
}

// Read returns every claim in the log, oldest first. A missing log is empty;
// a torn or unparsable line is skipped.
func Read(repoDir string) []Claim {
	f, err := os.Open(logPath(repoDir))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Claim
	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var c Claim
			if json.Unmarshal(line, &c) == nil && c.Branch != "" && c.Objective != "" {
				out = append(out, c)
			}
		}
		if err != nil {
			return out
		}
	}
}

// ForBranch returns the newest claim for branch, if any.
func ForBranch(repoDir, branch string) (Claim, bool) {
	var found Claim
	ok := false
	for _, c := range Read(repoDir) {
		if c.Branch == branch {
			found, ok = c, true
		}
	}
	return found, ok
}

// Add appends a claim of objective for the current branch. A branch that
// already claims a different objective is refused unless force is set: new
// work belongs in a new session on a new branch. Re-claiming the same
// objective (a resumed session) appends a line so the new session is recorded.
func Add(repoDir, objective string, force bool) (Claim, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" || strings.ContainsAny(objective, " \t\n") {
		return Claim{}, fmt.Errorf("objective must be one bead id, got %q", objective)
	}
	branch := gitutil.Repo{Dir: repoDir}.CurrentBranch()
	if branch == "" || branch == "HEAD" {
		return Claim{}, errors.New("not on a branch: create the objective's branch first")
	}
	if prev, ok := ForBranch(repoDir, branch); ok && prev.Objective != objective && !force {
		return prev, fmt.Errorf("%w: %s works toward %s; start a new session on a new branch for %s, or pass -force",
			ErrOtherObjective, branch, prev.Objective, objective)
	}
	c := Claim{
		Time:         time.Now().UTC().Format(time.RFC3339),
		Branch:       branch,
		Objective:    objective,
		Session:      strings.TrimSpace(os.Getenv("CLAUDE_CODE_SESSION_ID")), // usage.SessionEnv
		AgentSession: strings.TrimSpace(os.Getenv("AGENTDECK_INSTANCE_ID")),
	}
	p := logPath(repoDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return c, err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return c, err
	}
	defer f.Close()
	b, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	_, err = f.Write(append(b, '\n'))
	return c, err
}

// Source says where a branch's objective came from.
type Source string

const (
	FromClaim   Source = "claim"
	FromCommits Source = "commits"
	// FromBranchName is a bead id the branch is named after
	// (feat/irisux-7-scenes → PROJ-irisux-7). Agents name branches after the
	// bead far more often than they run the claim command.
	FromBranchName Source = "branch"
)

// Current is the objective the current branch works toward: its claim, else
// the first `objective:` named by its own commits (those not on the remote's
// default branch), else a bead the branch is named after. Branch is "" outside
// a branch.
type Current struct {
	Branch    string
	Objective string
	Source    Source
}

// Resolve finds the current branch's objective.
func Resolve(repoDir string) Current {
	repo := gitutil.Repo{Dir: repoDir}
	cur := Current{Branch: repo.CurrentBranch()}
	if cur.Branch == "" || cur.Branch == "HEAD" {
		cur.Branch = ""
		return cur
	}
	if c, ok := ForBranch(repoDir, cur.Branch); ok {
		cur.Objective, cur.Source = c.Objective, FromClaim
		return cur
	}
	if base := repo.DefaultRemoteBranch(); base != "" {
		commits, _ := repo.Log(repo.MergeBase(base, "HEAD"), "HEAD") // oldest first
		for _, c := range commits {
			if id := trailers.Parse(c.Body).Objective; id != "" {
				cur.Objective, cur.Source = id, FromCommits
				return cur
			}
		}
	}
	if st, err := beads.ReadDir(repoDir); err == nil {
		if id := BeadInBranch(cur.Branch, st); id != "" {
			cur.Objective, cur.Source = id, FromBranchName
		}
	}
	return cur
}

// BeadInBranch returns the bead a branch is named after, or "". A bead
// matches when its id, or its id without the store's leading prefix segment
// (PROJ-irisux-7 → irisux-7), appears in the branch name between separators.
// The short form must carry a letter and a digit, so a bare number never
// matches. A match contained in a longer match yields to it (the plan
// PROJ-irisux inside feat/PROJ-irisux-7); two separate beads named in one
// branch are ambiguous and match nothing.
func BeadInBranch(branch string, st beads.Store) string {
	b := strings.ToLower(branch)
	type hit struct{ id, form string }
	var hits []hit
	for _, it := range st.Issues {
		for _, form := range idForms(it.ID) {
			if f := strings.ToLower(form); boundedIn(b, f) {
				hits = append(hits, hit{it.ID, f})
			}
		}
	}
	winner := ""
	for _, h := range hits {
		subsumed := false
		for _, o := range hits {
			if o.id != h.id && len(o.form) > len(h.form) && strings.Contains(o.form, h.form) {
				subsumed = true
				break
			}
		}
		if subsumed || h.id == winner {
			continue
		}
		if winner != "" {
			return ""
		}
		winner = h.id
	}
	return winner
}

func idForms(id string) []string {
	forms := []string{id}
	if _, short, ok := strings.Cut(id, "-"); ok && strings.ContainsAny(short, "0123456789") &&
		strings.ContainsAny(short, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		forms = append(forms, short)
	}
	return forms
}

// boundedIn reports whether needle occurs in s with a separator (or the end
// of s) on both sides, so irisux-7 matches feat/irisux-7-scenes but not
// feat/irisux-70.
func boundedIn(s, needle string) bool {
	sep := func(c byte) bool { return c == '/' || c == '-' || c == '_' || c == '.' }
	for i := 0; ; {
		j := strings.Index(s[i:], needle)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(needle)
		if (start == 0 || sep(s[start-1])) && (end == len(s) || sep(s[end])) {
			return true
		}
		i = start + 1
	}
}

// Stamp returns msg with `objective: <id>` appended when the message names no
// objective, and reports what it did. A message that names a different
// objective is left alone and reported, so the hook can warn about it.
func Stamp(msg, objective string) (out string, added bool, other string) {
	body := msg
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		body = msg[i+1:]
	} else {
		body = ""
	}
	if named := trailers.Parse(stripComments(body)).Objective; named != "" {
		if named != objective {
			return msg, false, named
		}
		return msg, false, ""
	}
	// Insert before git's comment block (an editor commit), so the line is
	// not stripped as a comment along with it.
	content, comments := splitComments(msg)
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return msg, false, "" // an empty message aborts the commit; leave it so
	}
	sep := "\n\n"
	if lastParagraphIsTrailers(content) {
		sep = "\n"
	}
	return content + sep + "objective: " + objective + "\n" + comments, true, ""
}

// splitComments separates a message from git's trailing "#" comment block.
func splitComments(msg string) (content, comments string) {
	lines := strings.SplitAfter(msg, "\n")
	cut := len(lines)
	for cut > 0 {
		l := strings.TrimSpace(lines[cut-1])
		if l != "" && !strings.HasPrefix(l, "#") {
			break
		}
		cut--
	}
	return strings.Join(lines[:cut], ""), strings.Join(lines[cut:], "")
}

func stripComments(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if !strings.HasPrefix(l, "#") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

// lastParagraphIsTrailers reports whether the message ends in a block of
// "Key: value" lines (a git trailer block or a nugit capture block), so the
// objective joins it rather than opening a paragraph of its own.
func lastParagraphIsTrailers(content string) bool {
	paras := strings.Split(content, "\n\n")
	if len(paras) < 2 {
		return false // the subject alone
	}
	last := strings.TrimSpace(paras[len(paras)-1])
	if last == "" {
		return false
	}
	for _, l := range strings.Split(last, "\n") {
		k, _, ok := strings.Cut(l, ": ")
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return false
		}
	}
	return true
}

// Bead is the store's view of an objective, for the digest.
type Bead struct {
	ID, Title, Status string
	Found             bool
}

// Lookup finds id in the working tree's plan store.
func Lookup(st beads.Store, id string) Bead {
	it, ok := st.Find(id)
	if !ok {
		return Bead{ID: id}
	}
	return Bead{ID: id, Title: it.Title, Status: it.Status, Found: true}
}
