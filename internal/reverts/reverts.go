// Package reverts reports how often merged changes were reverted, per month,
// with a size-matched set of changes that stayed clean (ADR-0059 §1). It reads
// a ref's first-parent history and nothing else: no network, no clock ("now"
// is the tip's commit time), so the same ref always gives the same report.
package reverts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/gitutil"
)

const (
	maxChanges = 10000 // first-parent commits read for the window
	maxReverts = 500   // revert commits resolved (each costs up to three git calls)
	cleanDays  = 30    // a change is clean when nothing reverted or fixed it this long
)

// Change is one merged change: a first-parent commit of the ref.
type Change struct {
	SHA     string    `json:"sha"`
	Merged  time.Time `json:"merged"`
	Subject string    `json:"subject"`
	Files   int       `json:"files"`
	Churn   int       `json:"churn"`
}

// Month is one calendar month of merged changes, keyed YYYY-MM by merge month.
type Month struct {
	Month               string  `json:"month"`
	Merged              int     `json:"merged"`
	Reverted            int     `json:"reverted"`
	Rate                float64 `json:"rate"`
	MedianChurn         int     `json:"median_churn"`
	RevertedMedianChurn int     `json:"reverted_median_churn,omitempty"`
}

// Revert is a merged change and the revert that undid it.
type Revert struct {
	Commit   string `json:"commit"` // the revert commit itself
	Landed   Change `json:"landed"` // the first-parent change that brought the revert in
	Reverted Change `json:"reverted"`
	AgeDays  int    `json:"age_days"`
}

// Control is the clean change size-matched to a reverted one; Change is nil
// when the month has no clean change left to match.
type Control struct {
	For    string  `json:"for"`
	Change *Change `json:"change"`
}

// Report is the whole answer.
type Report struct {
	Ref       string    `json:"ref"`
	Tip       string    `json:"tip"`
	Now       time.Time `json:"now"`
	Since     time.Time `json:"since"`
	Months    []Month   `json:"months"`
	Reverts   []Revert  `json:"reverts"`
	Controls  []Control `json:"controls"`
	Truncated bool      `json:"truncated,omitempty"`
}

// Edge is one revert commit, resolved to the first-parent changes on both
// ends. Reland is true when the reverted commit was itself a revert.
type Edge struct {
	Commit, Landed, Reverted string
	Reland                   bool
}

// Options for Run.
type Options struct {
	RepoDir string
	Ref     string // "" reads origin's default branch
	Months  int    // window in calendar months, counting the tip's; default 6
}

var (
	revertRE = regexp.MustCompile(`This reverts commit ([0-9a-f]{7,40})`)
	fixRE    = regexp.MustCompile(`^fix(\([^)]*\))?!?:`)
)

// Run reads the ref's history and builds the report.
func Run(o Options) (Report, error) {
	repo := gitutil.Repo{Dir: o.RepoDir}
	if o.Ref == "" {
		if o.Ref = repo.DefaultRemoteBranch(); o.Ref == "" {
			return Report{}, fmt.Errorf("no -ref, and the clone records no origin default branch (refs/remotes/origin/HEAD); pass -ref <branch>, or run `git remote set-head origin -a`")
		}
	}
	if o.Months <= 0 {
		o.Months = 6
	}
	tip, err := repo.FirstParentLog(o.Ref, time.Time{}, 1)
	if err != nil || len(tip) == 0 {
		return Report{}, fmt.Errorf("reading %s: %v", o.Ref, err)
	}
	now := tip[0].Time
	since := windowStart(now, o.Months)
	changes, err := repo.FirstParentLog(o.Ref, since, maxChanges)
	if err != nil {
		return Report{}, err
	}
	commits, err := repo.RevertLog(o.Ref, since, maxReverts)
	if err != nil {
		return Report{}, err
	}
	onLine := map[string]bool{}
	for _, c := range changes {
		onLine[c.SHA] = true
	}
	isRevert := map[string]bool{}
	for _, rc := range commits {
		isRevert[rc.SHA] = true
	}
	cache := map[string]string{}
	changeOf := func(sha string) string {
		if onLine[sha] {
			return sha
		}
		if v, ok := cache[sha]; ok {
			return v
		}
		cache[sha] = repo.FirstParentContaining(o.Ref, sha, maxChanges)
		return cache[sha]
	}
	var edges []Edge
	for _, rc := range commits {
		for _, m := range revertRE.FindAllStringSubmatch(rc.Body, -1) {
			if full := repo.Resolve(m[1]); full != "" {
				edges = append(edges, Edge{Commit: rc.SHA, Landed: changeOf(rc.SHA), Reverted: changeOf(full), Reland: isRevert[full]})
			}
		}
	}
	rep := Build(changes, edges, now, o.Months)
	rep.Ref, rep.Tip = o.Ref, tip[0].SHA
	rep.Truncated = len(changes) >= maxChanges || len(commits) >= maxReverts
	return rep, nil
}

// windowStart is the first instant of the oldest month in a window of months
// calendar months ending with now's.
func windowStart(now time.Time, months int) time.Time {
	return time.Date(now.Year(), now.Month()-time.Month(months-1), 1, 0, 0, 0, 0, time.UTC)
}

// Build is the report over already-read history: changes from FirstParentLog
// (any order) and revert edges. It is pure, so it is what the tests drive.
func Build(changes []gitutil.FirstParentChange, edges []Edge, now time.Time, months int) Report {
	since := windowStart(now, months)
	rep := Report{Now: now, Since: since, Reverts: []Revert{}, Controls: []Control{}}
	byID := map[string]gitutil.FirstParentChange{}
	var window []gitutil.FirstParentChange
	for _, c := range changes {
		byID[c.SHA] = c
		if !c.Time.Before(since) && !c.Time.After(now) {
			window = append(window, c)
		}
	}
	sort.Slice(window, func(i, j int) bool {
		if !window[i].Time.Equal(window[j].Time) {
			return window[i].Time.Before(window[j].Time)
		}
		return window[i].SHA < window[j].SHA
	})
	inWindow := map[string]bool{}
	for _, c := range window {
		inWindow[c.SHA] = true
	}

	// Reverts: one per reverted change, the earliest to land. A re-land, a
	// revert made inside the same change, and one that cannot be placed on the
	// line are not reverts of a merged change.
	landedRevert := map[string]bool{}
	first := map[string]Revert{}
	for _, e := range edges {
		landed, ok := byID[e.Landed]
		if !ok || e.Reland || e.Landed == e.Reverted || !inWindow[e.Reverted] {
			continue
		}
		landedRevert[e.Landed] = true
		r := Revert{Commit: e.Commit, Landed: view(landed), Reverted: view(byID[e.Reverted]),
			AgeDays: int(landed.Time.Sub(byID[e.Reverted].Time).Hours() / 24)}
		if prev, ok := first[e.Reverted]; !ok || r.Landed.Merged.Before(prev.Landed.Merged) {
			first[e.Reverted] = r
		}
	}
	for _, r := range first {
		rep.Reverts = append(rep.Reverts, r)
	}
	sort.Slice(rep.Reverts, func(i, j int) bool {
		a, b := rep.Reverts[i].Reverted, rep.Reverts[j].Reverted
		if !a.Merged.Equal(b.Merged) {
			return a.Merged.Before(b.Merged)
		}
		return a.SHA < b.SHA
	})

	// Clean: not reverted, not itself a revert, no fix touching its files
	// within cleanDays, and old enough at the tip for that to be known.
	fixesByPath := map[string][]time.Time{}
	for _, c := range changes {
		if fixRE.MatchString(title(c)) {
			for _, p := range c.Files {
				fixesByPath[p] = append(fixesByPath[p], c.Time)
			}
		}
	}
	horizon := cleanDays * 24 * time.Hour
	clean := func(c gitutil.FirstParentChange) bool {
		if _, reverted := first[c.SHA]; reverted || landedRevert[c.SHA] || len(c.Files) == 0 ||
			strings.Contains(c.Body, "This reverts commit") || now.Sub(c.Time) < horizon {
			return false
		}
		for _, p := range c.Files {
			for _, t := range fixesByPath[p] {
				if t.After(c.Time) && !t.After(c.Time.Add(horizon)) {
					return false
				}
			}
		}
		return true
	}
	used := map[string]bool{}
	for _, r := range rep.Reverts {
		var best *gitutil.FirstParentChange
		for i := range window {
			c := &window[i]
			if used[c.SHA] || month(c.Time) != month(r.Reverted.Merged) || !clean(*c) {
				continue
			}
			if best == nil || closer(*c, *best, r.Reverted) {
				best = c
			}
		}
		ctl := Control{For: r.Reverted.SHA}
		if best != nil {
			used[best.SHA] = true
			v := view(*best)
			ctl.Change = &v
		}
		rep.Controls = append(rep.Controls, ctl)
	}

	// Months, oldest first, every month of the window present.
	for m := since; !m.After(now); m = m.AddDate(0, 1, 0) {
		key := month(m)
		var churn, rchurn []int
		for _, c := range window {
			if month(c.Time) == key {
				churn = append(churn, c.Churn)
			}
		}
		for _, r := range rep.Reverts {
			if month(r.Reverted.Merged) == key {
				rchurn = append(rchurn, r.Reverted.Churn)
			}
		}
		mo := Month{Month: key, Merged: len(churn), Reverted: len(rchurn), MedianChurn: median(churn), RevertedMedianChurn: median(rchurn)}
		if mo.Merged > 0 {
			mo.Rate = float64(mo.Reverted) / float64(mo.Merged)
		}
		rep.Months = append(rep.Months, mo)
	}
	return rep
}

// closer reports whether a is a better size match for target than b: nearer
// churn, then nearer file count, then the lower SHA, so the pick is stable.
func closer(a, b gitutil.FirstParentChange, target Change) bool {
	if da, db := abs(a.Churn-target.Churn), abs(b.Churn-target.Churn); da != db {
		return da < db
	}
	if da, db := abs(len(a.Files)-target.Files), abs(len(b.Files)-target.Files); da != db {
		return da < db
	}
	return a.SHA < b.SHA
}

// title is what a change calls itself: its subject, or for a merge commit
// ("Merge pull request #12 from …") the first line of its body, which is the
// PR's title, so a merged `fix:` PR reads as a fix.
func title(c gitutil.FirstParentChange) string {
	if strings.HasPrefix(c.Subject, "Merge ") {
		if line, _, _ := strings.Cut(c.Body, "\n"); strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return c.Subject
}

func view(c gitutil.FirstParentChange) Change {
	return Change{SHA: c.SHA, Merged: c.Time, Subject: title(c), Files: len(c.Files), Churn: c.Churn}
}

func month(t time.Time) string { return t.UTC().Format("2006-01") }

func median(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	if n := len(s); n%2 == 0 {
		return (s[n/2-1] + s[n/2]) / 2
	}
	return s[len(s)/2]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Markdown renders the report for a terminal or a CI summary.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Reverts per month — %s at %s (%s)\n\n", r.Ref, short(r.Tip), r.Now.Format("2006-01-02"))
	b.WriteString("| month | merged | reverted | rate | median churn | reverted churn (median) |\n|---|---:|---:|---:|---:|---:|\n")
	for _, m := range r.Months {
		rc := "—"
		if m.Reverted > 0 {
			rc = fmt.Sprint(m.RevertedMedianChurn)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %.1f%% | %d | %s |\n", m.Month, m.Merged, m.Reverted, 100*m.Rate, m.MedianChurn, rc)
	}
	b.WriteString("\n## Reverts\n\n")
	if len(r.Reverts) == 0 {
		b.WriteString("None in the window.\n")
	}
	for _, v := range r.Reverts {
		fmt.Fprintf(&b, "- `%s` %s reverted `%s` %s (merged %s, %d files, %d lines) after %d days\n",
			short(v.Landed.SHA), v.Landed.Merged.Format("2006-01-02"), short(v.Reverted.SHA), v.Reverted.Subject,
			v.Reverted.Merged.Format("2006-01-02"), v.Reverted.Files, v.Reverted.Churn, v.AgeDays)
	}
	if len(r.Controls) > 0 {
		b.WriteString("\n## Size-matched clean changes\n\n")
		for _, c := range r.Controls {
			if c.Change == nil {
				fmt.Fprintf(&b, "- for `%s`: no clean change left in its month\n", short(c.For))
				continue
			}
			fmt.Fprintf(&b, "- for `%s`: `%s` %s (%d files, %d lines)\n", short(c.For), short(c.Change.SHA), c.Change.Subject, c.Change.Files, c.Change.Churn)
		}
	}
	fmt.Fprintf(&b, "\n_Months are by when a change merged, so a month's rate can still rise as late reverts land. A change is clean when nothing reverted it and no `fix` commit touched its files within %d days of merging._\n", cleanDays)
	if r.Truncated {
		b.WriteString("\n_The history read hit its cap; the oldest months may be incomplete. Narrow `-months`._\n")
	}
	return b.String()
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
