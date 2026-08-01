package consistency

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/evidence"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/trailers"
)

// RecurrenceOpts configure the recurrence check (config recurrence.*).
// Recurrence is the strongest capture signal the pilot data shows (ADR-0019):
// the same component keeps being fixed while the knowledge store stays silent.
type RecurrenceOpts struct {
	Enabled    bool
	WindowDays int // history window, scanned backwards from now
	MinFixes   int // fix-typed commits on one component that trigger the warn
	// Now anchors the knowledge-capture window (testable); zero -> time.Now().
	Now time.Time
}

// maxScanCommits bounds each per-component git log so the scan stays
// O(window), never O(history), even on pathological churn.
const maxScanCommits = 200

// fixSubjectRE matches conventional-commit fix-typed subjects — fix:, fix(x):,
// fix!:, revert:, revert(x): — case-insensitively.
var fixSubjectRE = regexp.MustCompile(`(?i)^(fix|revert)(\([^)]*\))?!?:`)

// fixTyped reports whether a commit subject is fix-typed: a conventional
// fix/revert, or git's default `Revert "…"` subject.
func fixTyped(subject string) bool {
	return fixSubjectRE.MatchString(subject) || strings.HasPrefix(subject, `Revert "`)
}

// checkRecurrence: a component this PR touches has accumulated >= MinFixes
// fix-typed commits inside the window with no knowledge delta — the failure
// class is recurring and nothing durable was captured (ADR-0019). Warn only:
// the evidence lives outside (base, head], so it must nudge, never block or
// reclassify the PR under review.
func checkRecurrence(in Input) []model.Finding {
	o := in.Recurrence
	if !o.Enabled || o.WindowDays <= 0 || o.MinFixes <= 0 {
		return nil
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	windowStart := now.AddDate(0, 0, -o.WindowDays)

	// Live knowledge per governed component, and whether any of it was created
	// inside the window (a fresh capture closes the loop for the whole window).
	governing := map[string][]model.KnowledgeObject{}
	captured := map[string]bool{}
	for _, obj := range in.AllObjects {
		if obj.ID == "" || obj.EffectiveStatus == model.StatusSuperseded || obj.EffectiveStatus == model.StatusInvalidated {
			continue
		}
		for _, comp := range evidence.GovernedComponents(obj) {
			governing[comp] = append(governing[comp], obj)
			if !obj.Created.IsZero() && !obj.Created.Before(windowStart) {
				captured[comp] = true
			}
		}
	}

	touchedSet := map[string]bool{}
	for _, fc := range in.Code.Files {
		if fc.Component != "" {
			touchedSet[fc.Component] = true
		}
	}
	touched := make([]string, 0, len(touchedSet))
	for c := range touchedSet {
		touched = append(touched, c)
	}
	sort.Strings(touched)

	var fs []model.Finding
	for _, comp := range touched {
		if captured[comp] {
			continue
		}
		globs := elementPaths(in.HeadModel, comp)
		if len(globs) == 0 {
			continue
		}
		commits, err := in.Repo.LogSince(in.Head, o.WindowDays, maxScanCommits, globs...)
		if err != nil {
			continue // a history-scan error must never fail the render
		}
		var fixes []model.Commit
		trailerCapture := false
		for _, c := range commits {
			// A learned:/decision: trailer on ANY in-window commit touching the
			// component counts as capture (ADR-0005: the trailer is the capture
			// primitive), not just on the fix-typed ones.
			tr := trailers.Parse(c.Body)
			if strings.TrimSpace(tr.Learned) != "" || strings.TrimSpace(tr.Decision) != "" {
				trailerCapture = true
				break
			}
			if fixTyped(c.Subject) {
				fixes = append(fixes, c)
			}
		}
		if trailerCapture || len(fixes) < o.MinFixes {
			continue
		}
		fs = append(fs, model.Finding{
			Check:    "recurrence",
			Severity: model.SevWarn,
			Title:    fmt.Sprintf("%d fix-typed commits on %s in %dd with no knowledge captured", len(fixes), comp, o.WindowDays),
			Detail:   recurrenceDetail(fixes, governing[comp]),
		})
	}
	return fs
}

// elementPaths returns the path globs binding an element id (component or
// container) to files.
func elementPaths(m model.Model, id string) []string {
	if c, ok := m.Comp(id); ok {
		return c.Paths
	}
	if ct, ok := m.Container(id); ok {
		return ct.Paths
	}
	return nil
}

// recurrenceDetail lists the recurring fixes and points at the remediation:
// reinforcement when governing knowledge already exists (it proved too
// narrow), fresh capture when nothing governs the component at all.
func recurrenceDetail(fixes []model.Commit, governing []model.KnowledgeObject) string {
	var b strings.Builder
	b.WriteString("the same failure class keeps recurring with nothing durable captured: ")
	show := fixes
	if len(show) > 5 {
		show = show[:5]
	}
	parts := make([]string, 0, len(show))
	for _, c := range show {
		s := c.Subject
		if len(s) > 60 {
			s = s[:60] + "…"
		}
		parts = append(parts, fmt.Sprintf("%s %q", short(c.SHA), s))
	}
	b.WriteString(strings.Join(parts, "; "))
	if n := len(fixes) - len(show); n > 0 {
		fmt.Fprintf(&b, "; and %d more", n)
	}
	ids := governingIDs(governing)
	if len(ids) > 0 {
		fmt.Fprintf(&b, ". Governing knowledge exists (%s) but predates the window — if the lesson proved too narrow, widen it with `nugit reinforce %s -text \"…\" -keywords …` (ADR-0019), or capture a new one (learned:/keywords: trailer + `nugit distill`)",
			strings.Join(ids, ", "), ids[0])
	} else {
		b.WriteString(". No knowledge object governs this component — capture the why with a `learned:`/`decision:` trailer (promoted by `nugit distill`) or author an ADR/lesson")
	}
	return b.String()
}

// governingIDs returns up to three sorted, deduplicated live object ids.
func governingIDs(objs []model.KnowledgeObject) []string {
	set := map[string]bool{}
	for _, o := range objs {
		if o.ID != "" {
			set[o.ID] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 3 {
		ids = ids[:3]
	}
	return ids
}
