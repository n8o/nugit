package adopt

// Runbook candidates (ADR-0036).
//
// A shelf of symptom-keyed runbooks is a knowledge store that nobody can query:
// the documents are lessons in prose, retrievable only by someone who already
// knows the file exists. They are the highest-value thing to import — and the
// easiest to get wrong, because a lesson whose Trigger was invented is
// indistinguishable from one that was observed.
//
// So: detect the candidates, extract what is deterministically there, and where
// the trigger or the insight is NOT there, say so. ADR-0028 already settled that
// argument for `nugit distill`; this reuses its placeholder rather than minting
// a second one, and asks the ADR-0027 export gate — not a third local lexicon —
// whether a line reads as a symptom.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/n8o/nugit/internal/distill"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/skillopt"
)

// Runbook is one symptom-keyed document that could become a lesson.
type Runbook struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	// Why the document was picked: "runbooks-dir" | "symptom-title".
	Detected string `json:"detected_by"`
	// Trigger is the observable symptom, or "" when none could be extracted.
	Trigger       string `json:"trigger,omitempty"`
	TriggerSource string `json:"trigger_source,omitempty"` // "section:Symptom" | "title"
	// Insight is the recorded resolution, or "" when none could be extracted.
	Insight       string `json:"insight,omitempty"`
	InsightSource string `json:"insight_source,omitempty"`
	// Gaps names what could not be extracted. A candidate with gaps is still
	// reported — refusing to fabricate is the point, hiding the candidate is not.
	Gaps []string `json:"gaps,omitempty"`
}

// Complete reports whether both halves of a lesson were found in the document.
func (r Runbook) Complete() bool { return len(r.Gaps) == 0 }

// Section headings are matched by KEYWORD, not by exact title: real runbooks
// write "Likely cause", "Root cause", "What to try" and "Remediation steps" for
// the same two slots, and an exact-match list would need a new entry per
// house style. One keyword anywhere in the heading is enough.

// symptomSectionWords head the observation half.
var symptomSectionWords = map[string]bool{
	"symptom": true, "symptoms": true, "trigger": true, "triggers": true,
	"problem": true, "detection": true, "detect": true, "signal": true,
	"signals": true, "observed": true, "observation": true, "alert": true,
	"impact": true, "failure": true,
}

// resolutionSectionWords head the answer half.
var resolutionSectionWords = map[string]bool{
	"resolution": true, "resolve": true, "fix": true, "remediation": true,
	"remediate": true, "recovery": true, "recover": true, "mitigation": true,
	"mitigate": true, "cause": true, "causes": true, "diagnosis": true,
	"diagnose": true, "action": true, "actions": true, "steps": true,
	"procedure": true, "insight": true, "workaround": true, "try": true,
}

// sectionMatch reports whether a heading names one of the wanted slots.
func sectionMatch(heading string, want map[string]bool) (string, bool) {
	h := strings.ToLower(strings.Trim(strings.TrimSpace(heading), ":"))
	for _, w := range strings.FieldsFunc(h, func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	}) {
		if want[w] {
			return h, true
		}
	}
	return h, false
}

var (
	labelRE   = regexp.MustCompile(`^\*{0,2}([A-Za-z][A-Za-z -]{1,20})\*{0,2}\s*:\s*(.*)$`)
	slugRE    = regexp.MustCompile(`[^a-z0-9]+`)
	runbookRE = regexp.MustCompile(`(?i)^(runbooks?|playbooks?)$`)
)

// runbookCandidates finds the symptom-keyed documents in the corpus.
func runbookCandidates(repoDir string, docs []docText) []Runbook {
	var out []Runbook
	for _, d := range docs {
		if isInventoryRoot(d.Path) {
			continue
		}
		detected := ""
		for _, seg := range strings.Split(path.Dir(d.Path), "/") {
			if runbookRE.MatchString(seg) {
				detected = "runbooks-dir"
			}
		}
		title := docTitle(d)
		if detected == "" {
			if skillopt.LooksLikeSymptom(title) {
				detected = "symptom-title"
			} else {
				continue
			}
		}
		rb := Runbook{Path: d.Path, Title: title, Detected: detected}
		rb.Trigger, rb.TriggerSource = extractTrigger(d, title)
		rb.Insight, rb.InsightSource = extractSection(d, resolutionSectionWords)
		if rb.Trigger == "" {
			rb.Gaps = append(rb.Gaps, "no observable symptom found (no Symptom/Trigger/Problem section, and the title does not read as one)")
		}
		if rb.Insight == "" {
			rb.Gaps = append(rb.Gaps, "no resolution found (no Resolution/Fix/Cause section)")
		}
		out = append(out, rb)
	}
	return out
}

func isInventoryRoot(p string) bool {
	for _, r := range inventoryRoots {
		if p == r {
			return true
		}
	}
	return false
}

// docTitle is the first H1, else the filename humanized ("volume-pending.md" ->
// "volume pending") — which is exactly how a symptom-keyed shelf is filed.
func docTitle(d docText) string {
	for _, raw := range d.Lines {
		t := strings.TrimSpace(raw)
		if m := headingRE.FindStringSubmatch(t); m != nil && len(m[1]) == 1 {
			return strings.TrimSpace(m[2])
		}
	}
	base := strings.TrimSuffix(path.Base(d.Path), path.Ext(d.Path))
	return strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(base))
}

// extractTrigger prefers a Symptom-ish section; failing that the title, but only
// if the title itself reads as an observation. It never falls back to anything
// else — the placeholder is the honest answer (ADR-0028).
func extractTrigger(d docText, title string) (string, string) {
	if s, src := extractSection(d, symptomSectionWords); s != "" && skillopt.LooksLikeSymptom(s) {
		return s, src
	}
	if skillopt.LooksLikeSymptom(title) {
		return title, "title"
	}
	return "", ""
}

// extractSection returns the first content line under the first heading (or
// inline `**Label:**` line) whose name is in want, plus where it came from.
// One line, not a paragraph: a lesson header is a sentence, and a runbook's
// procedure section is a page.
func extractSection(d docText, want map[string]bool) (string, string) {
	inFence := false
	capture, label := false, ""
	for _, raw := range d.Lines {
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := headingRE.FindStringSubmatch(t); m != nil {
			label, capture = sectionMatch(m[2], want)
			continue
		}
		if !capture {
			// An inline "**Symptom:** pods crashloop" carries both halves on one
			// line — the compact form a short runbook uses instead of headings.
			if m := labelRE.FindStringSubmatch(t); m != nil {
				if name, ok := sectionMatch(m[1], want); ok {
					if v := cleanLine(m[2]); v != "" {
						return v, "section:" + name
					}
				}
			}
			continue
		}
		if v := cleanLine(t); v != "" {
			return v, "section:" + label
		}
	}
	return "", ""
}

// cleanLine strips list markers, blockquote markers and emphasis, and rejects
// the leftovers that are structure rather than content.
func cleanLine(t string) string {
	t = strings.TrimSpace(t)
	if t == "" || strings.HasPrefix(t, "|") || strings.HasPrefix(t, "<") {
		return ""
	}
	if m := listMarkerRE.FindStringSubmatch(t); m != nil {
		t = m[3]
	}
	t = strings.TrimSpace(strings.TrimPrefix(t, ">"))
	t = strings.Trim(t, "*_ ")
	if len([]rune(t)) > 400 {
		t = strings.TrimSpace(string([]rune(t)[:400]))
	}
	return t
}

// --- the one thing adopt writes ---------------------------------------------

// writeCandidates lands the runbook candidates in the CANDIDATE LANE
// (.nugit/lessons/, `status: proposed` — ADR-0016), never in the enforced text.
// A candidate whose symptom could not be extracted gets distill's TriggerTODO
// placeholder verbatim, so an un-filled lesson is visible in review and is
// refused by the ADR-0027 export gate by construction.
//
// Existing files are never clobbered, matching distill's writer.
func writeCandidates(repoDir string, rbs []Runbook, now string) ([]string, error) {
	if len(rbs) == 0 {
		return nil, nil
	}
	dir := filepath.Join(repoDir, ".nugit", "lessons")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	stamp := nowStamp(now)
	used := map[string]bool{}
	var written []string
	for _, rb := range rbs {
		s := uniqueSlug("adopt-"+slugify(rb.Title), used, dir)
		used[s] = true
		p := filepath.Join(dir, s+".md")
		if _, err := os.Stat(p); err == nil {
			continue // never clobber
		}
		if err := os.WriteFile(p, []byte(candidateBody(rb, s, stamp)), 0o644); err != nil {
			return written, err
		}
		written = append(written, filepath.ToSlash(filepath.Join(".nugit", "lessons", s+".md")))
	}
	return written, nil
}

func candidateBody(rb Runbook, slug, now string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nschema_version: 1\nid: %s\ntype: lesson\nscope: global\nstatus: %s\ncreated: %s\nprovenance:\n  citation: %q\nconfidence: low\n---\n\n",
		"LESSON-"+slug, model.StatusProposed, now, rb.Path)
	fmt.Fprintf(&b, "# Lesson — %s\n\n", rb.Title)
	trigger := rb.Trigger
	if trigger == "" {
		trigger = distill.TriggerTODO + " (imported from " + rb.Path + "; no Symptom/Trigger section, and the title does not read as an observation)"
	}
	fmt.Fprintf(&b, "**Trigger:** %s\n\n", trigger)
	insight := rb.Insight
	if insight == "" {
		insight = "TODO — the root cause and fix (imported from " + rb.Path + "; no Resolution/Fix/Cause section found)"
	}
	fmt.Fprintf(&b, "**Insight:** %s\n\n", insight)
	fmt.Fprintf(&b, "**Keywords:** %s\n\n", strings.Join(keywordsFor(rb), ", "))
	fmt.Fprintf(&b, "_Proposed by `nugit adopt -write-candidates` from %s. Unratified: review the Trigger and Insight against the source document, then `nugit ratify %s`._\n",
		rb.Path, "LESSON-"+slug)
	return b.String()
}

// keywordsFor seeds retrieval keywords from the title and the source path —
// both authored by a human, neither invented here.
func keywordsFor(rb Runbook) []string {
	seen := map[string]bool{}
	var out []string
	add := func(w string) {
		w = strings.ToLower(strings.Trim(w, "-"))
		if len(w) < 3 || seen[w] || docVocab[w] {
			return
		}
		seen[w] = true
		out = append(out, w)
	}
	for _, w := range tokenRE.FindAllString(rb.Title, -1) {
		add(w)
	}
	base := strings.TrimSuffix(path.Base(rb.Path), path.Ext(rb.Path))
	for _, w := range strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
		add(w)
	}
	if len(out) == 0 {
		out = append(out, "runbook")
	}
	return out
}

func slugify(s string) string {
	s = slugRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 50 {
		s = strings.Trim(s[:50], "-")
	}
	if s == "" {
		s = "runbook"
	}
	return s
}

// uniqueSlug disambiguates two candidates that humanize to the same title
// within one run. It does NOT rename around a file that already exists — the
// caller skips those, so a second run over the same shelf writes nothing.
func uniqueSlug(base string, used map[string]bool, _ string) string {
	s := base
	for i := 2; used[s]; i++ {
		s = base + "-" + strconv.Itoa(i)
	}
	return s
}
