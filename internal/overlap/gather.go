package overlap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
)

// DefaultRecent is how recently a branch with no open pull request must have
// been pushed to count as open work.
const DefaultRecent = 14 * 24 * time.Hour

// GatherOptions configure Gather.
type GatherOptions struct {
	// Base is the target branch; "" reads refs/remotes/origin/HEAD.
	Base string
	// Remote holds the pushed branches; "" is "origin".
	Remote string
	// Recent admits a pushed branch with no open pull request when its tip
	// is this new; 0 admits none.
	Recent time.Duration
	// NoGitHub skips asking gh for open pull requests.
	NoGitHub bool
	// Exclude names branches that are the reviewed change itself, by head
	// name ("feat/x") or remote ref ("origin/feat/x").
	Exclude []string
	Now     time.Time
}

// Gathered is the open work Gather found, and how.
type Gathered struct {
	// Base is the resolved target branch.
	Base   string
	Works  []model.OpenWork
	Source string
	// Unread names the open pull requests whose heads this checkout lacks.
	Unread []string
}

// ErrNoBase is returned when the target branch cannot be determined.
var ErrNoBase = errors.New("cannot tell the target branch: pass -base, or run `git remote set-head origin --auto`")

// Gather lists the open work in the repo at repoDir: the open pull requests
// gh reports, and the branches on the remote pushed within Recent that have
// none. It only reads, and only what is already fetched: a pull request whose
// head is not in this checkout is named in Unread, never fetched.
func Gather(repoDir string, opt GatherOptions) (Gathered, error) {
	repo := gitutil.Repo{Dir: repoDir}
	if opt.Remote == "" {
		opt.Remote = "origin"
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	g := Gathered{Base: opt.Base}
	if g.Base == "" {
		g.Base = repo.DefaultRemoteBranch()
	}
	baseTip := repo.Resolve(g.Base)
	if g.Base == "" || baseTip == "" {
		return g, ErrNoBase
	}
	excluded := map[string]bool{}
	for _, e := range opt.Exclude {
		if e != "" {
			excluded[e] = true
			excluded[opt.Remote+"/"+e] = true
		}
	}

	var prs []pullRequest
	gh := false
	if !opt.NoGitHub {
		prs, gh = listPRs(repoDir)
	}
	// A branch other work targets is a line others merge into (the default
	// branch, a release or integration branch), not a change of its own.
	targets := map[string]bool{g.Base: true}
	if def := repo.DefaultRemoteBranch(); def != "" {
		targets[def] = true
	}
	prHeads := map[string]bool{}
	for _, pr := range prs {
		prHeads[opt.Remote+"/"+pr.Head] = true
		if pr.BaseRef != "" {
			targets[opt.Remote+"/"+pr.BaseRef] = true
		}
	}
	for _, pr := range prs {
		if excluded[pr.Head] {
			continue
		}
		w := model.OpenWork{PR: pr.Number, Title: pr.Title, Draft: pr.Draft, Branch: pr.Head}
		switch {
		case pr.Oid != "" && repo.Resolve(pr.Oid) != "":
			w.Ref = pr.Oid
		case !pr.Fork && repo.Resolve(opt.Remote+"/"+pr.Head) != "":
			w.Ref = opt.Remote + "/" + pr.Head
		default:
			g.Unread = append(g.Unread, w.Label())
			continue
		}
		if b := opt.Remote + "/" + pr.BaseRef; pr.BaseRef != "" && repo.Resolve(b) != "" {
			w.Base = b
		}
		g.Works = append(g.Works, w)
	}

	if opt.Recent > 0 {
		branches, err := repo.RemoteBranches(opt.Remote)
		if err != nil {
			return g, err
		}
		// A branch left behind by a closed or merged pull request is finished
		// work, not open work: closed unmerged, or merged in a way
		// ContentMerged cannot see once the base has moved past it.
		var done finishedPRs
		if gh {
			done = listFinished(repoDir)
		}
		for _, b := range branches {
			if targets[b.Name] || b.Tip == baseTip || prHeads[b.Name] || excluded[b.Name] ||
				done.left(opt.Remote, b) || opt.Now.Sub(b.Time) > opt.Recent {
				continue
			}
			// Gone into the base already: fast-forwarded, or squash-merged
			// with the branch left behind.
			if repo.Ahead(g.Base, b.Name) == 0 || repo.ContentMerged(g.Base, b.Name) {
				continue
			}
			g.Works = append(g.Works, model.OpenWork{Branch: b.Name, Pushed: b.Time.Format("2006-01-02"), Ref: b.Name})
		}
	}

	days := int(opt.Recent / (24 * time.Hour))
	switch {
	case gh && opt.Recent > 0:
		g.Source = fmt.Sprintf("open pull requests (gh), and branches on %s pushed in the last %d days with none", opt.Remote, days)
	case gh:
		g.Source = "open pull requests (gh)"
	case opt.Recent > 0:
		g.Source = fmt.Sprintf("branches on %s pushed in the last %d days (no pull requests: gh was not asked or did not answer)", opt.Remote, days)
	default:
		g.Source = "nothing: gh did not answer and no branch window was given"
	}
	return g, nil
}

// pullRequest is the slice of `gh pr list` Gather reads.
type pullRequest struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Head    string `json:"headRefName"`
	Oid     string `json:"headRefOid"`
	BaseRef string `json:"baseRefName"`
	Draft   bool   `json:"isDraft"`
	Fork    bool   `json:"isCrossRepository"`
}

// listPRs and listFinished are openPRs and finishedTips; a test swaps
// them for fixed answers.
var (
	listPRs      = openPRs
	listFinished = finishedTips
)

// finishedLimit bounds the closed and merged pull requests read: the newest
// ones, which is where a recently pushed branch's would be.
const finishedLimit = 500

// finishedPRs is what the recent closed and merged pull requests left: the
// head commit each ended at, and when each branch name's last one ended.
type finishedPRs struct {
	tips  map[string]bool
	ended map[string]time.Time // head branch name -> latest close or merge
}

// left reports whether b is a finished pull request's branch: still at the
// commit the PR ended at, or with a tip no newer than the PR's close. A
// branch committed to after its PR ended is new work, and stays.
func (f finishedPRs) left(remote string, b gitutil.Branch) bool {
	if f.tips[b.Tip] {
		return true
	}
	t, ok := f.ended[strings.TrimPrefix(b.Name, remote+"/")]
	return ok && !b.Time.After(t)
}

// finishedTips reads the recent closed or merged pull requests (gh's
// "closed" includes merged). Empty when gh does not answer.
func finishedTips(repoDir string) finishedPRs {
	f := finishedPRs{tips: map[string]bool{}, ended: map[string]time.Time{}}
	bin, err := exec.LookPath("gh")
	if err != nil {
		return f
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "pr", "list", "--state", "closed", "--limit", fmt.Sprint(finishedLimit),
		"--json", "headRefName,headRefOid,closedAt")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return f
	}
	return parseFinished(out)
}

func parseFinished(b []byte) finishedPRs {
	f := finishedPRs{tips: map[string]bool{}, ended: map[string]time.Time{}}
	var rows []struct {
		Head   string    `json:"headRefName"`
		Oid    string    `json:"headRefOid"`
		Closed time.Time `json:"closedAt"`
	}
	if json.Unmarshal(b, &rows) != nil {
		return f
	}
	for _, r := range rows {
		if r.Oid != "" {
			f.tips[r.Oid] = true
		}
		if r.Head != "" && !r.Closed.IsZero() && r.Closed.After(f.ended[r.Head]) {
			f.ended[r.Head] = r.Closed
		}
	}
	return f
}

// prLimit bounds the open pull requests read.
const prLimit = 1000

// openPRs asks gh for the open pull requests. ok is false when gh is not
// installed, not logged in, or the repo has no GitHub remote, so the report
// says it read branches only rather than claiming no PR is open.
func openPRs(repoDir string) (prs []pullRequest, ok bool) {
	bin, err := exec.LookPath("gh")
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "pr", "list", "--state", "open", "--limit", fmt.Sprint(prLimit),
		"--json", "number,title,headRefName,headRefOid,baseRefName,isDraft,isCrossRepository")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	return parsePRs(out)
}

func parsePRs(b []byte) ([]pullRequest, bool) {
	var prs []pullRequest
	if json.Unmarshal(b, &prs) != nil {
		return nil, false
	}
	kept := prs[:0]
	for _, pr := range prs {
		if pr.Number > 0 && strings.TrimSpace(pr.Head) != "" {
			kept = append(kept, pr)
		}
	}
	return kept, true
}
