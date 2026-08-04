// Package skillopt projects nugit's git-authoritative knowledge store OUTBOUND
// as skill-optimizer eval cases (ADR-0027 / ADR-0011): git is the source of
// truth, the benchmark corpus a DERIVED artifact regenerated from it. A
// hand-mined corpus freezes on its mining date; a derived one grows with the
// repo, so capture improves the store and the store regenerates the benchmark.
//
// The output is JSONL — one case per line: a stable id, the title/source as
// harness-only metadata, the observable symptom as `input` (the sole field the
// model under test sees), and root cause + fix as `gold_answer`.
//
// Deterministic and LLM-free, like every other nugit projection: same store →
// same corpus, byte for byte. The correctness burden is therefore entirely on
// the leakage gate in gate.go — see ADR-0027.
package skillopt

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// Case is one eval case — one JSONL line. Field names and shapes match the
// consuming benchmark's corpus format.
type Case struct {
	// Number is the stable case id ("lesson:<slug>"): stable across
	// regenerations so a consumer can hash it into a train/val/test split that
	// survives the corpus growing.
	Number string `json:"number"`
	// Title and Source are harness metadata — NEVER shown to the model under
	// test, and never allowed to bleed into Input (they routinely state the
	// conclusion).
	Title  string `json:"title"`
	Source string `json:"source"`
	// Input is the observable symptom, built ONLY from the Trigger section.
	Input string `json:"input"`
	// GoldAnswer is root cause + fix, with the Rejected rationale appended as
	// negative guidance when the lesson carries one.
	GoldAnswer string `json:"gold_answer"`
	// Labels drive the harness's per-category score breakdown.
	Labels []string `json:"labels"`
}

// Refusal records one object the gate would not turn into a case, and why.
type Refusal struct {
	Number  string   `json:"number"`
	Source  string   `json:"source"`
	Reasons []string `json:"reasons"`
}

// Summary counts an export by tier, mirroring `nugit deploy`'s inventory report.
type Summary struct {
	Lessons    int            `json:"lessons_scanned"`
	Emitted    int            `json:"emitted"`
	High3      int            `json:"high_3_signal"`
	High2      int            `json:"high_2_signal"`
	NeedsAgent int            `json:"needs_agent"`
	Reasons    map[string]int `json:"refusal_reasons,omitempty"`
}

// Report is the full accounting of an export: what was emitted, and every
// object that was refused with its reasons.
type Report struct {
	Summary Summary   `json:"summary"`
	Refused []Refusal `json:"refused,omitempty"`
}

// Options configure an export.
type Options struct {
	RepoDir string
	// MinTier gates emission: "" / "high-2" (default) emits both tiers,
	// "high-3" emits only rich triggers and reports thin ones as refused.
	MinTier string
}

// Export reads the store and returns the emittable cases plus the report. Cases
// are sorted by Number so the corpus is byte-stable across runs.
func Export(opt Options) ([]Case, Report, error) {
	minTier, err := normalizeTier(opt.MinTier)
	if err != nil {
		return nil, Report{}, err
	}
	objs, err := knowledge.Load(opt.RepoDir)
	if err != nil {
		return nil, Report{}, err
	}
	var cases []Case
	rep := Report{Summary: Summary{Reasons: map[string]int{}}}
	for i := range objs {
		o := objs[i]
		if o.Type != model.KindLesson {
			continue
		}
		rep.Summary.Lessons++
		c, v := Assess(o)
		if v.Tier == TierHigh2 && minTier == TierHigh3 {
			v = Verdict{Tier: TierNeedsAgent, Reasons: []string{ReasonThinTrigger}}
		}
		if !v.Emitted() {
			rep.Summary.NeedsAgent++
			for _, r := range v.Reasons {
				rep.Summary.Reasons[r]++
			}
			rep.Refused = append(rep.Refused, Refusal{Number: c.Number, Source: c.Source, Reasons: v.Reasons})
			continue
		}
		c.Labels = append(c.Labels, "tier:"+strings.ToLower(v.Tier))
		switch v.Tier {
		case TierHigh3:
			rep.Summary.High3++
		default:
			rep.Summary.High2++
		}
		rep.Summary.Emitted++
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Number < cases[j].Number })
	sort.Slice(rep.Refused, func(i, j int) bool { return rep.Refused[i].Number < rep.Refused[j].Number })
	if len(rep.Summary.Reasons) == 0 {
		rep.Summary.Reasons = nil
	}
	return cases, rep, nil
}

// Assess turns one knowledge object into its prospective Case and the gate's
// verdict on it. The Case is returned even when refused, so the report can name
// the object — but a refused Case is never emitted.
//
// The single most important line in this package is the one that sets Input:
// it reads the Trigger section and NOTHING else. Title, id, filename and
// keywords all routinely state the conclusion.
func Assess(o model.KnowledgeObject) (Case, Verdict) {
	s := ParseSections(o.Body)
	number := caseNumber(o)
	title := titleOf(o)
	c := Case{
		Number:     number,
		Title:      title,
		Source:     storeRelPath(o.Path),
		Input:      s.Trigger, // ONLY the trigger — see the doc comment above
		GoldAnswer: goldAnswer(s),
		Labels:     labelsFor(o),
	}
	if stale := statusRefusal(o); stale != "" {
		return c, Verdict{Tier: TierNeedsAgent, Reasons: []string{stale}}
	}
	return c, grade(candidate{
		Trigger:  c.Input,
		Answer:   s.Answer(),
		Titleish: title + " " + strings.ReplaceAll(number, "-", " "),
		Keywords: s.Keywords,
	})
}

// statusRefusal excludes objects whose answer is not trustworthy ground truth:
// a superseded or invalidated lesson holds a DEAD answer, and a proposed one is
// an unreviewed machine draft (the ADR-0016 candidate lane). Both would train
// the wrong thing.
func statusRefusal(o model.KnowledgeObject) string {
	switch o.EffectiveStatus {
	case model.StatusSuperseded, model.StatusInvalidated:
		return ReasonStale
	case model.StatusProposed:
		return ReasonUnratified
	}
	return ""
}

// goldAnswer is root cause + fix, with the Rejected rationale appended as
// explicit negative guidance ("what not to do") — the anti-hallucination half
// of a nugit lesson is exactly what a graded answer should contain.
func goldAnswer(s Sections) string {
	a := s.Answer()
	if a == "" {
		return ""
	}
	if s.Rejected != "" {
		a += "\n\nDo not: " + s.Rejected
	}
	return a
}

// labelsFor tags a case for the harness's per-category breakdown: its type, and
// its component scope so a run can be scored per area of the system.
func labelsFor(o model.KnowledgeObject) []string {
	labels := []string{string(model.KindLesson)}
	if s := strings.TrimSpace(o.Scope); s != "" {
		labels = append(labels, "scope:"+s)
	}
	return labels
}

// caseNumber is the stable case id: "lesson:<slug>", from the object id with its
// LESSON- prefix stripped, else the filename.
func caseNumber(o model.KnowledgeObject) string {
	slug := strings.TrimSpace(o.ID)
	if slug == "" {
		slug = strings.TrimSuffix(filepath.Base(o.Path), ".md")
	}
	low := strings.ToLower(slug)
	low = strings.TrimPrefix(low, "lesson-")
	return "lesson:" + low
}

// storeRelPath is the object's path relative to the .nugit store root, so the
// corpus is portable across checkouts.
func storeRelPath(p string) string {
	p = filepath.ToSlash(p)
	if i := strings.Index(p, ".nugit/"); i >= 0 {
		return p[i+len(".nugit/"):]
	}
	return p
}

// titleOf is the object's first markdown heading, with the "Lesson — " lead-in
// dropped, else its id. Metadata only: the harness does not show it to the model.
func titleOf(o model.KnowledgeObject) string {
	for _, line := range strings.Split(o.Body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			t = strings.TrimSpace(strings.TrimLeft(t, "# "))
			for _, dash := range []string{" — ", " – ", " - "} {
				if i := strings.Index(t, dash); i >= 0 && strings.EqualFold(strings.TrimSpace(t[:i]), "lesson") {
					t = strings.TrimSpace(t[i+len(dash):])
					break
				}
			}
			return t
		}
	}
	return o.ID
}

func normalizeTier(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "high-2", "high2":
		return TierHigh2, nil
	case "high-3", "high3":
		return TierHigh3, nil
	}
	return "", fmt.Errorf("skillopt: unknown -min-tier %q (want high-2 or high-3)", s)
}

// WriteJSONL writes one JSON object per line. HTML escaping is off so prose
// containing <ref> or & stays readable; the output is still strict JSON.
func WriteJSONL(w io.Writer, cases []Case) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, c := range cases {
		if c.Labels == nil {
			c.Labels = []string{}
		}
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

// SummaryLines is the human-readable export report, for stderr — so stdout
// carries nothing but pipeable JSONL.
func (r Report) SummaryLines() string {
	s := r.Summary
	var b strings.Builder
	fmt.Fprintf(&b, "skillopt: %d lesson(s) scanned → %d case(s) emitted (%d×%s, %d×%s), %d×%s\n",
		s.Lessons, s.Emitted, s.High3, TierHigh3, s.High2, TierHigh2, s.NeedsAgent, TierNeedsAgent)
	if len(s.Reasons) > 0 {
		keys := make([]string, 0, len(s.Reasons))
		for k := range s.Reasons {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if s.Reasons[keys[i]] != s.Reasons[keys[j]] {
				return s.Reasons[keys[i]] > s.Reasons[keys[j]]
			}
			return keys[i] < keys[j]
		})
		b.WriteString("  refusals by reason:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "    %-24s %d\n", k, s.Reasons[k])
		}
	}
	for _, ref := range r.Refused {
		fmt.Fprintf(&b, "    skipped %-44s %s\n", ref.Number, strings.Join(ref.Reasons, ", "))
	}
	return b.String()
}
