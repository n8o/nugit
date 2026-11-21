// Package hygiene reports the rules a knowledge store carries that have
// stopped earning their place (ADR-0058). Rules only grow unless someone
// prunes them, and the store gives no signal on its own: a lesson nobody is
// shown and a checklist item no change has matched cost a reader the same as
// one that fires every day.
//
// A rule here is a lesson; a checklist item is a lesson with a diff trigger
// (ADR-0057). Three signals, all deterministic and local:
//
//   - unused: not served by any context() call in the window (the ADR-0013
//     usage log), and for a checklist item not matched by any commit in it;
//   - dead paths: an applies_to_paths or diff_trigger glob that matches no
//     tracked file, or a path the body cites that is no longer in the tree;
//   - never fired: a checklist item no commit since it was written has
//     matched, within the scan's bound.
//
// The report names candidates. Pruning is a person's call, made in a PR.
package hygiene

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/n8o/nugit/internal/checklist"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/usage"
)

// Options bound the report.
type Options struct {
	RepoDir string
	Days    int // the unused window (default 90)
	// MaxCommits caps each history scan (default 2000), so the report stays
	// O(window) however old the repo is.
	MaxCommits int
	Now        time.Time
	// Ref is the history matched against, default origin's default branch,
	// else HEAD.
	Ref string
}

// Rule is one rule the report names, with why.
type Rule struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Item   bool   `json:"checklist_item,omitempty"`
	Detail string `json:"detail"`
}

// Report is the hygiene report.
type Report struct {
	Days  int    `json:"days"`
	Ref   string `json:"ref"`
	Rules int    `json:"rules"`
	Items int    `json:"checklist_items"`
	// RetrievalSince is the earliest usage record that names what it served.
	// Zero means the log has none.
	RetrievalSince time.Time `json:"retrieval_since,omitempty"`
	// CoverageDays is how many days of the unused window the served-id log
	// covers, at most Days. Until it is Days, no rule is called unused:
	// UnusedHeld counts the rules not served or matched so far, and
	// JudgedFrom is when the log will cover the window.
	CoverageDays float64   `json:"retrieval_coverage_days"`
	UnusedHeld   int       `json:"unused_held,omitempty"`
	JudgedFrom   time.Time `json:"unused_judged_from,omitempty"`
	Scanned      int       `json:"commits_scanned"`
	Capped       bool      `json:"capped,omitempty"`
	Unused       []Rule    `json:"unused,omitempty"`
	DeadPaths    []Rule    `json:"dead_paths,omitempty"`
	NeverFired   []Rule    `json:"never_fired,omitempty"`
}

// Run builds the report for the store at opt.RepoDir.
func Run(opt Options) (Report, error) {
	if opt.Days <= 0 {
		opt.Days = 90
	}
	if opt.MaxCommits <= 0 {
		opt.MaxCommits = 2000
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now().UTC()
	}
	repo := gitutil.Repo{Dir: opt.RepoDir}
	if opt.Ref == "" {
		opt.Ref = repo.DefaultRemoteBranch()
		if opt.Ref == "" {
			opt.Ref = "HEAD"
		}
	}
	objs, err := knowledge.Load(opt.RepoDir)
	if err != nil {
		return Report{}, err
	}
	rules := liveLessons(objs)
	rep := Report{Days: opt.Days, Ref: opt.Ref, Rules: len(rules)}
	items := checklist.Items(rules, "")
	rep.Items = len(items)
	if len(rules) == 0 {
		return rep, nil
	}
	prefix := repo.Prefix()

	// Unused: served by no context() call in the window.
	records, _ := usage.Read(opt.RepoDir)
	since := opt.Now.AddDate(0, 0, -opt.Days)
	served := map[string]bool{}
	for _, r := range records {
		if !r.IsCall() || len(r.Served) == 0 {
			continue
		}
		if rep.RetrievalSince.IsZero() || r.Time.Before(rep.RetrievalSince) {
			rep.RetrievalSince = r.Time
		}
		if !r.Time.Before(since) {
			for _, id := range r.Served {
				served[id] = true
			}
		}
	}

	// "Unused in N days" needs N days of served ids. A log that began inside
	// the window has not had the chance to see a rule served, so the list
	// waits for it.
	covered := false
	if !rep.RetrievalSince.IsZero() {
		covered = !rep.RetrievalSince.After(since)
		rep.CoverageDays = float64(opt.Days)
		if !covered {
			rep.CoverageDays = math.Round(opt.Now.Sub(rep.RetrievalSince).Hours()/24*10) / 10
			rep.JudgedFrom = rep.RetrievalSince.AddDate(0, 0, opt.Days)
		}
	}

	// Matched: the items the window's commits fired, and, for never-fired,
	// the items any commit since the oldest item was written fired.
	matchedWindow, matchedEver := map[string]bool{}, map[string]bool{}
	if len(items) > 0 {
		paths := triggerPaths(items)
		oldest := opt.Now
		for _, it := range items {
			if !it.Created.IsZero() && it.Created.Before(oldest) {
				oldest = it.Created
			}
		}
		days := int(opt.Now.Sub(oldest).Hours()/24) + 1
		if days < opt.Days {
			days = opt.Days
		}
		commits, err := repo.LogPatch(opt.Ref, days, opt.MaxCommits, paths)
		if err == nil {
			rep.Scanned, rep.Capped = len(commits), len(commits) >= opt.MaxCommits
			for _, c := range commits {
				lines := checklist.ParseDiff(c.Diff)
				files := make([]string, 0, len(lines))
				for f := range lines {
					files = append(files, f)
				}
				for _, m := range checklist.Match(items, files, lines) {
					created := createdOf(items, m.ID)
					if !c.Time.Before(created) {
						matchedEver[m.ID] = true
					}
					if !c.Time.Before(since) {
						matchedWindow[m.ID] = true
					}
				}
			}
		}
	}

	tree, _ := repo.ListTree("HEAD")
	tracked := make(map[string]bool, len(tree))
	tops := map[string]bool{}
	byBase := map[string][]string{} // file name -> tracked paths, for tail matches
	for _, p := range tree {
		tracked[p] = true
		if i := strings.IndexByte(p, '/'); i > 0 {
			tops[p[:i]] = true
		}
		byBase[lastSeg(p)] = append(byBase[lastSeg(p)], p)
	}

	for _, o := range rules {
		isItem := o.DiffTrigger != nil
		// Too young to judge: a rule written inside the window has not had
		// the window to be used in.
		young := !o.Created.IsZero() && o.Created.After(since)
		if !young && !served[o.ID] && !matchedWindow[o.ID] {
			if !covered {
				rep.UnusedHeld++
			} else {
				d := fmt.Sprintf("served by no context() call in %d days", opt.Days)
				if isItem {
					d += " and matched by no commit in them"
				}
				rep.Unused = append(rep.Unused, Rule{ID: o.ID, Path: o.Path, Item: isItem, Detail: d})
			}
		}
		if dead := deadPaths(o, prefix, tracked, tops, byBase); len(dead) > 0 {
			rep.DeadPaths = append(rep.DeadPaths, Rule{ID: o.ID, Path: o.Path, Item: isItem,
				Detail: strings.Join(dead, "; ")})
		}
		if isItem && !young && !matchedEver[o.ID] && len(tree) > 0 {
			d := "no commit since it was written matches its trigger"
			if !o.Created.IsZero() {
				d = "no commit since " + o.Created.Format("2006-01-02") + " matches its trigger"
			}
			if rep.Capped {
				d += fmt.Sprintf(" (scan capped at %d commits)", opt.MaxCommits)
			}
			rep.NeverFired = append(rep.NeverFired, Rule{ID: o.ID, Path: o.Path, Item: true, Detail: d})
		}
	}
	return rep, nil
}

// liveLessons is the store's local lessons that are not superseded or
// invalidated: the rules a reader is still asked to follow.
func liveLessons(objs []model.KnowledgeObject) []model.KnowledgeObject {
	var out []model.KnowledgeObject
	for _, o := range objs {
		if o.Foreign() || o.Type != model.KindLesson || o.ID == "" {
			continue
		}
		st := o.EffectiveStatus
		if st == "" {
			st = o.Status
		}
		if st == model.StatusSuperseded || st == model.StatusInvalidated {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func createdOf(items []model.KnowledgeObject, id string) time.Time {
	for _, it := range items {
		if it.ID == id {
			return it.Created
		}
	}
	return time.Time{}
}

// triggerPaths is the literal directory prefix of each trigger glob, so the
// history scan reads only commits that could match. A glob with a wildcard in
// its first segment scans everything.
func triggerPaths(items []model.KnowledgeObject) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		for _, g := range it.DiffTrigger.Paths {
			p := literalPrefix(g)
			if p == "" {
				return nil
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func literalPrefix(glob string) string {
	segs := strings.Split(strings.TrimPrefix(glob, "./"), "/")
	var keep []string
	for _, s := range segs {
		if strings.ContainsAny(s, "*?[{") {
			break
		}
		keep = append(keep, s)
	}
	return strings.Join(keep, "/")
}

// citedPath matches a backticked repo path in a record body: two or more
// segments, the last with an extension or a glob.
var citedPath = regexp.MustCompile("`([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.*-]+)+)`")

// deadPaths names what a rule points at that the tree no longer has: binding
// globs that match no tracked file, and body-cited paths under a top-level
// directory the tree still has that are themselves gone. The top-level
// filter keeps prose like `a/b` or another repo's paths out of it.
//
// A cited path is relative to wherever its author was working: a lesson
// about one app's UI tests cites `tests/ui/README.md`, meaning the one under
// that app. So a cite is the TAIL of a path, and it is dead only when no
// tracked file under the nugit root is it or ends with it. Resolving against
// the record's scope alone would miss both common cases: a component bound
// to `<app>/src/**` citing `<app>/tests/…`, and a global lesson citing a
// file deep in one app. A cite with an elided segment (`helm/.../x.yaml`)
// names no single path, so it is not judged.
func deadPaths(o model.KnowledgeObject, prefix string, tracked map[string]bool, tops map[string]bool, byBase map[string][]string) []string {
	if len(tracked) == 0 {
		return nil
	}
	var dead []string
	globs := append([]string(nil), o.AppliesToPaths...)
	field := map[string]string{}
	for _, g := range o.AppliesToPaths {
		field[g] = "applies_to_paths"
	}
	if o.DiffTrigger != nil {
		for _, g := range o.DiffTrigger.Paths {
			globs = append(globs, g)
			field[g] = "diff_trigger.paths"
		}
	}
	for _, g := range globs {
		if !doublestar.ValidatePattern(g) || matchesAny(g, tracked) {
			continue
		}
		dead = append(dead, fmt.Sprintf("%s %q matches no tracked file", field[g], g))
	}
	seen := map[string]bool{}
	for _, m := range citedPath.FindAllStringSubmatch(o.Body, -1) {
		p := strings.TrimSuffix(m[1], ".")
		if seen[p] || strings.ContainsAny(p, "*") || !strings.Contains(lastSeg(p), ".") {
			continue
		}
		seen[p] = true
		if elided(p) {
			continue
		}
		full := prefix + p
		top := full[:strings.IndexByte(full, '/')]
		if !tops[top] || tracked[full] || hasTail(byBase[lastSeg(p)], prefix, p) {
			continue
		}
		dead = append(dead, fmt.Sprintf("cites `%s`, which is no longer in the tree", p))
	}
	return dead
}

// num renders a day count: "0.5", or "12" when it is whole.
func num(d float64) string {
	if d == float64(int(d)) {
		return fmt.Sprintf("%d", int(d))
	}
	return fmt.Sprintf("%.1f", d)
}

// elided reports a cite with a "..." segment, written to shorten a path.
func elided(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "..." {
			return true
		}
	}
	return false
}

// hasTail reports a tracked path under prefix that ends with "/"+tail.
func hasTail(candidates []string, prefix, tail string) bool {
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) && strings.HasSuffix(c, "/"+tail) {
			return true
		}
	}
	return false
}

func matchesAny(glob string, tracked map[string]bool) bool {
	for p := range tracked {
		if ok, _ := doublestar.Match(glob, p); ok {
			return true
		}
	}
	return false
}

func lastSeg(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Markdown renders the report for the terminal.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## nugit stats — rule hygiene (ADR-0058)\n\n")
	fmt.Fprintf(&b, "- **rules**: %d live lesson(s), %d of them checklist items\n", r.Rules, r.Items)
	if r.RetrievalSince.IsZero() {
		b.WriteString("- **retrieval data**: none yet — the usage log names served ids from this version on\n")
	} else {
		fmt.Fprintf(&b, "- **retrieval data**: since %s, %s of the %d-day window\n",
			r.RetrievalSince.Format("2006-01-02 15:04"), num(r.CoverageDays), r.Days)
	}
	if r.Items > 0 {
		capped := ""
		if r.Capped {
			capped = " (capped)"
		}
		fmt.Fprintf(&b, "- **history matched**: %d commit(s) on %s%s\n", r.Scanned, r.Ref, capped)
	}
	section := func(title, hint string, rules []Rule) {
		if len(rules) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n### %s (%d)\n\n_%s_\n\n", title, len(rules), hint)
		for _, x := range rules {
			kind := ""
			if x.Item {
				kind = " ☑️"
			}
			fmt.Fprintf(&b, "- `%s`%s — %s\n", x.ID, kind, x.Detail)
		}
	}
	section(fmt.Sprintf("unused in %d days", r.Days),
		"supersede, merge into a rule that is used, or delete it", r.Unused)
	if r.UnusedHeld > 0 {
		fmt.Fprintf(&b, "\n### unused in %d days — held\n\n", r.Days)
		if r.RetrievalSince.IsZero() {
			fmt.Fprintf(&b, "_The usage log has no served ids yet, so no rule is judged unused. "+
				"%d rule(s) have not been served or matched so far._\n", r.UnusedHeld)
		} else {
			fmt.Fprintf(&b, "_The usage log names served ids for %s of %d days (since %s). "+
				"%d rule(s) have not been served or matched so far; they are judged once the log "+
				"covers the window, on %s._\n", num(r.CoverageDays), r.Days,
				r.RetrievalSince.Format("2006-01-02 15:04"), r.UnusedHeld, r.JudgedFrom.Format("2006-01-02"))
		}
	}
	section("dead paths", "point the binding at where the code went, or retire the rule with it", r.DeadPaths)
	section("checklist items that never fired",
		"loosen the trigger if it should have fired, or delete the item", r.NeverFired)
	if len(r.Unused)+len(r.DeadPaths)+len(r.NeverFired)+r.UnusedHeld == 0 {
		b.WriteString("\n_Nothing to prune._\n")
	}
	return b.String()
}
