package orphans

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/claim"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/localmem"
	"github.com/n8o/nugit/internal/trailers"
)

// Options configure Gather.
type Options struct {
	// Base overrides the default branch ref ("origin/main"); "" reads
	// refs/remotes/origin/HEAD.
	Base  string
	Stale time.Duration
	// NoSessions skips asking agent-deck for session state.
	NoSessions bool
	// NoGitHub skips asking gh for the branches' pull requests.
	NoGitHub bool
	Now      time.Time
}

// historyDepth bounds how much of the default branch is read for commits
// naming an objective.
const historyDepth = 2000

// ErrNoBase is returned when the default branch cannot be determined.
var ErrNoBase = errors.New("cannot tell origin's default branch: pass -base, or run `git remote set-head origin --auto`")

// Gather reads everything Compute needs from the repo at repoDir (a nugit
// root). It only reads: nothing is fetched, written or deleted.
func Gather(repoDir string, opt Options) (Input, error) {
	repo := gitutil.Repo{Dir: repoDir}
	def := opt.Base
	if def == "" {
		def = repo.DefaultRemoteBranch()
	}
	if def == "" || repo.Resolve(def) == "" {
		return Input{}, ErrNoBase
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	in := Input{Now: opt.Now, Stale: opt.Stale, Default: def, Named: map[string]time.Time{}}
	in.Store, _ = beads.Read(repo, def, repo.Prefix())
	in.Claims = claim.Read(repoDir)
	in.Notes = localmem.Recent(repoDir, 1000)

	note := func(cs []gitutil.TimedCommit) (first string) {
		for _, c := range cs { // newest first, so the last match is the first named
			id := trailers.Parse(c.Body).Objective
			if id == "" {
				continue
			}
			first = id
			if c.Time.After(in.Named[id]) {
				in.Named[id] = c.Time
			}
		}
		return first
	}
	if cs, err := repo.LogTimed(def, historyDepth); err == nil {
		note(cs)
	}

	defTip := repo.Resolve(def)
	defShort := def[strings.IndexByte(def, '/')+1:]
	branches, err := repo.Branches()
	if err != nil {
		return in, err
	}
	for _, b := range branches {
		if b.Name == defShort || b.Tip == defTip {
			continue
		}
		br := Branch{Name: b.Name, Worktree: b.Worktree, Tip: b.Time, Ahead: repo.Ahead(def, b.Name)}
		if br.Ahead > 0 {
			br.ContentMerged = repo.ContentMerged(def, b.Name)
			if cs, err := repo.LogTimed(def+".."+b.Name, 500); err == nil {
				br.Objective = note(cs)
			}
		}
		in.Branches = append(in.Branches, br)
	}
	if !opt.NoGitHub {
		if prs := githubPRs(repoDir); prs != nil {
			in.GitHub = true
			for i := range in.Branches {
				if pr, ok := prs[in.Branches[i].Name]; ok {
					in.Branches[i].PR = &pr
				}
			}
		}
	}
	if !opt.NoSessions {
		in.Sessions = agentDeckSessions()
	}
	return in, nil
}

// prLimit bounds the pull requests read from GitHub (newest first). Too low
// a limit hides the PRs of old branches, which then show as never merged;
// listing 2,700 PRs takes about 15 seconds.
const prLimit = 10000

// githubPRs maps each head branch to its newest pull request, via gh. nil
// when gh is not installed, not logged in, or the repo has no GitHub remote,
// so the report falls back to what git alone can tell.
func githubPRs(repoDir string) map[string]PR {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "pr", "list", "--state", "all", "--limit", strconv.Itoa(prLimit),
		"--json", "number,state,headRefName")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parsePRs(out)
}

func parsePRs(b []byte) map[string]PR {
	var rows []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Head   string `json:"headRefName"`
	}
	if json.Unmarshal(b, &rows) != nil {
		return nil
	}
	m := map[string]PR{}
	for _, r := range rows { // gh lists newest first; the first PR per head wins
		if _, ok := m[r.Head]; !ok && r.Head != "" {
			m[r.Head] = PR{Number: r.Number, State: r.State}
		}
	}
	return m
}

// agentDeckSessions asks agent-deck for its sessions. nil when agent-deck is
// not installed or does not answer, so the report says it skipped the check
// rather than calling every claim's session stopped.
func agentDeckSessions() map[string]Session {
	bin, err := exec.LookPath("agent-deck")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "list", "-json")
	cmd.Env = append(os.Environ(), "AGENTDECK_SUPPRESS_TMUX_WARNING=1")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseSessions(out)
}

func parseSessions(b []byte) map[string]Session {
	var rows []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Status       string `json:"status"`
		LastActivity string `json:"last_activity_at"`
	}
	if json.Unmarshal(b, &rows) != nil {
		return nil
	}
	m := make(map[string]Session, len(rows))
	for _, r := range rows {
		m[r.ID] = Session{ID: r.ID, Title: r.Title, Status: r.Status, LastActivity: parseTime(r.LastActivity)}
	}
	return m
}
