// Package orphans finds work that was started toward an objective and then
// left (ADR-0048 phase 3): objectives that stalled, sessions that stopped with
// their objective open, branches with work no PR carries, beads closed while
// work stayed on a branch, and working notes never promoted.
//
// It is a report, not a gate: each orphan comes with the choice to make
// (finish it or drop it), and the agent carries out whatever the reviewer
// picks. Compute is pure over gathered inputs; Gather does the reading.
package orphans

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/claim"
	"github.com/n8o/nugit/internal/localmem"
)

// Kind names one class of orphan.
type Kind string

const (
	StalledObjective Kind = "stalled-objective"
	StoppedSession   Kind = "stopped-session"
	StrayBranch      Kind = "stray-branch"
	MergedBranch     Kind = "merged-branch"
	ClosedUnmerged   Kind = "closed-with-work-left"
	StaleNote        Kind = "stale-note"
)

// kindOrder is the report's section order: what most likely needs a decision
// first, housekeeping last.
var kindOrder = []Kind{StalledObjective, StoppedSession, ClosedUnmerged, StrayBranch, StaleNote, MergedBranch}

// Orphan is one finding.
type Orphan struct {
	Kind    Kind
	Subject string // a bead id, branch name, or note time
	Title   string `json:",omitempty"`
	Detail  string
	// Action is the decision to make, phrased as the two ways out.
	Action string
	// Idle is how long since the last sign of work; IdleDays is the same in
	// whole days, for the JSON.
	Idle     time.Duration `json:"-"`
	IdleDays int
}

// Branch is a local branch with what Gather learned about it.
type Branch struct {
	Name     string
	Worktree string `json:",omitempty"`
	Tip      time.Time
	Ahead    int
	// ContentMerged is true when the default branch already holds the
	// branch's content (a squash merge).
	ContentMerged bool
	// Objective is the first objective named by the branch's own commits.
	Objective string `json:",omitempty"`
	// PR is the newest pull request from this branch, when GitHub was asked.
	PR *PR `json:",omitempty"`
}

// PR is a pull request's number and state (OPEN, MERGED or CLOSED).
type PR struct {
	Number int
	State  string
}

// merged reports whether the branch's work reached the default branch: its
// PR merged (the only reliable test after a squash merge once the default
// branch has moved on), or its content is already there.
func (b Branch) merged() bool {
	return b.Ahead == 0 || b.ContentMerged || (b.PR != nil && b.PR.State == "MERGED")
}

// Session is an agent-deck session's state.
type Session struct {
	ID, Title, Status string
	LastActivity      time.Time
}

// Live reports whether the session is still working or waiting on input.
func (s Session) Live() bool {
	switch s.Status {
	case "running", "waiting", "starting", "idle":
		return true
	}
	return false
}

// Input is everything Compute reads.
type Input struct {
	Now   time.Time
	Stale time.Duration
	// Default is the default branch ref the store is read at ("origin/main").
	Default string
	Store   beads.Store
	Claims  []claim.Claim
	// Branches are local branches other than the default.
	Branches []Branch
	// Named maps an objective id to the newest commit time naming it, over
	// the default branch's recent history and every branch's own commits.
	Named map[string]time.Time
	// Sessions maps an agent-deck instance id to its session; nil when
	// agent-deck was not consulted, so stopped-session is skipped.
	Sessions map[string]Session
	Notes    []localmem.Entry
	// GitHub is true when Branches carry their PRs.
	GitHub bool
}

// Report is the result.
type Report struct {
	Default  string
	Stale    string
	Orphans  []Orphan
	Sessions bool // whether agent-deck sessions were checked
	GitHub   bool // whether pull requests were checked
}

// Compute finds the orphans.
func Compute(in Input) Report {
	rep := Report{Default: in.Default, Stale: days(in.Stale), Sessions: in.Sessions != nil, GitHub: in.GitHub}
	branchByName := map[string]Branch{}
	for _, b := range in.Branches {
		branchByName[b.Name] = b
	}
	// live work per objective: a branch with unmerged commits for it, or a
	// claim held by a live session.
	liveBranch := map[string]string{}
	for _, b := range in.Branches {
		if b.Objective != "" && !b.merged() {
			liveBranch[b.Objective] = b.Name
		}
	}
	latestClaim := map[string]claim.Claim{} // branch -> newest claim
	for _, c := range in.Claims {
		latestClaim[c.Branch] = c
	}
	claimsFor := map[string][]claim.Claim{} // objective -> current claims
	for _, c := range latestClaim {
		claimsFor[c.Objective] = append(claimsFor[c.Objective], c)
		if b, ok := branchByName[c.Branch]; ok && !b.merged() {
			liveBranch[c.Objective] = c.Branch
		}
	}
	liveSession := func(c claim.Claim) bool {
		if in.Sessions == nil || c.AgentSession == "" {
			return false
		}
		s, ok := in.Sessions[c.AgentSession]
		return ok && s.Live()
	}

	// Objectives: open beads that were worked and went quiet, and closed beads
	// that left work on a branch.
	for _, it := range in.Store.Issues {
		last, worked := lastActivity(it, in.Named[it.ID], claimsFor[it.ID])
		if beads.IsDone(it.Status) {
			if br, ok := liveBranch[it.ID]; ok {
				b := branchByName[br]
				rep.Orphans = append(rep.Orphans, Orphan{
					Kind: ClosedUnmerged, Subject: it.ID, Title: it.Title,
					Detail: fmt.Sprintf("closed on %s, but %s still has %s not on it", in.Default, br, plural(b.Ahead, "commit")),
					Action: fmt.Sprintf("merge what %s still holds in a follow-up PR, or delete the branch", br),
					Idle:   in.Now.Sub(b.Tip),
				})
			}
			continue
		}
		if !worked && !beads.IsActive(it.Status) {
			continue // never started: backlog, not an orphan
		}
		held := false
		for _, c := range claimsFor[it.ID] {
			if liveSession(c) {
				held = true
			}
		}
		idle := in.Now.Sub(last)
		if held || last.IsZero() || idle < in.Stale {
			continue
		}
		detail := fmt.Sprintf("%s, no work for %s", it.Status, days(idle))
		if br, ok := liveBranch[it.ID]; ok {
			detail += fmt.Sprintf("; unmerged work on %s", br)
		} else if !in.Named[it.ID].IsZero() {
			detail += "; earlier commits name it"
		}
		rep.Orphans = append(rep.Orphans, Orphan{
			Kind: StalledObjective, Subject: it.ID, Title: it.Title, Detail: detail,
			Action: "resume it in a new session, or close it as won't-do (or split what remains into a new bead)",
			Idle:   idle,
		})
	}

	// Sessions that stopped while their objective is still open.
	if in.Sessions != nil {
		done := map[string]bool{}
		known := map[string]bool{}
		title := map[string]string{}
		for _, it := range in.Store.Issues {
			known[it.ID] = true
			title[it.ID] = it.Title
			done[it.ID] = beads.IsDone(it.Status)
		}
		for _, br := range sortedKeys(latestClaim) {
			c := latestClaim[br]
			if c.AgentSession == "" || (known[c.Objective] && done[c.Objective]) {
				continue
			}
			s, ok := in.Sessions[c.AgentSession]
			if ok && s.Live() {
				continue
			}
			state := "no longer exists"
			idle := in.Now.Sub(parseTime(c.Time))
			if ok {
				state = s.Status
				if !s.LastActivity.IsZero() {
					idle = in.Now.Sub(s.LastActivity)
				}
			}
			name := c.AgentSession
			if ok && s.Title != "" {
				name = s.Title
			}
			action := fmt.Sprintf("start that session again (`agent-deck session start %s`), or close the bead as won't-do", c.AgentSession)
			if !ok {
				action = "resume the objective in a new session on " + br + ", or close the bead as won't-do"
			}
			rep.Orphans = append(rep.Orphans, Orphan{
				Kind: StoppedSession, Subject: c.Objective, Title: title[c.Objective],
				Detail: fmt.Sprintf("agent-deck session %q (%s) claimed it on %s and is %s", name, c.AgentSession, br, state),
				Action: action,
				Idle:   idle,
			})
		}
	}

	// Branches: unmerged work no live objective explains, and merged leftovers.
	for _, b := range in.Branches {
		idle := in.Now.Sub(b.Tip)
		where := b.Name
		if b.Worktree != "" {
			where += " (worktree " + b.Worktree + ")"
		}
		switch {
		case b.merged():
			if idle < in.Stale {
				continue
			}
			detail := where + ": everything on it is already on " + in.Default
			if b.PR != nil && b.PR.State == "MERGED" {
				detail = fmt.Sprintf("%s: merged as #%d", where, b.PR.Number)
			}
			rep.Orphans = append(rep.Orphans, Orphan{
				Kind: MergedBranch, Subject: b.Name, Detail: detail,
				Action: "delete the branch" + worktreeToo(b),
				Idle:   idle,
			})
		case idle >= in.Stale:
			obj := b.Objective
			if c, ok := latestClaim[b.Name]; ok {
				obj = c.Objective
			}
			detail := fmt.Sprintf("%s: %s not on %s, last commit %s ago", where, plural(b.Ahead, "commit"), in.Default, days(idle))
			if obj != "" {
				detail += ", objective " + obj
			} else {
				detail += ", no objective"
			}
			action := "open a PR for it, or delete the branch" + worktreeToo(b)
			switch {
			case b.PR != nil && b.PR.State == "OPEN":
				detail += fmt.Sprintf("; PR #%d is open", b.PR.Number)
				action = fmt.Sprintf("finish PR #%d, or close it and delete the branch", b.PR.Number)
			case b.PR != nil && b.PR.State == "CLOSED":
				detail += fmt.Sprintf("; PR #%d was closed without merging", b.PR.Number)
				action = "reopen the work in a new PR, or delete the branch" + worktreeToo(b)
			case in.GitHub:
				detail += "; no PR"
			}
			rep.Orphans = append(rep.Orphans, Orphan{
				Kind: StrayBranch, Subject: b.Name, Detail: detail, Action: action, Idle: idle,
			})
		}
	}

	// Notes jotted mid-session and never turned into knowledge.
	for _, e := range in.Notes {
		t := parseTime(e.Time)
		if t.IsZero() || in.Now.Sub(t) < in.Stale {
			continue
		}
		rep.Orphans = append(rep.Orphans, Orphan{
			Kind: StaleNote, Subject: e.Time, Detail: fmt.Sprintf("%s: %s", e.Kind, oneLine(e.Text)),
			Action: "record it as a decision or lesson (a trailer, then distill), or drop it",
			Idle:   in.Now.Sub(t),
		})
	}

	for i := range rep.Orphans {
		rep.Orphans[i].IdleDays = int(rep.Orphans[i].Idle.Hours() / 24)
	}
	rank := map[Kind]int{}
	for i, k := range kindOrder {
		rank[k] = i
	}
	sort.SliceStable(rep.Orphans, func(i, j int) bool {
		a, b := rep.Orphans[i], rep.Orphans[j]
		if a.Kind != b.Kind {
			return rank[a.Kind] < rank[b.Kind]
		}
		if a.Idle != b.Idle {
			return a.Idle > b.Idle
		}
		return a.Subject < b.Subject
	})
	return rep
}

// lastActivity is the newest sign of work on a bead: its store timestamp, a
// commit naming it, or a claim of it. worked is true when a commit or a claim
// shows it was ever started.
func lastActivity(it beads.Issue, named time.Time, claims []claim.Claim) (last time.Time, worked bool) {
	last = parseTime(it.Field("updated_at", "updated"))
	if named.After(last) {
		last = named
	}
	worked = !named.IsZero() || len(claims) > 0
	for _, c := range claims {
		if t := parseTime(c.Time); t.After(last) {
			last = t
		}
	}
	return last, worked
}

func worktreeToo(b Branch) string {
	if b.Worktree == "" {
		return ""
	}
	return " and remove its worktree"
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func days(d time.Duration) string {
	n := int(d.Hours() / 24)
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
