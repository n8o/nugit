package distill

// Trigger seeding for distilled lessons (ADR-0028).
//
// A nugit lesson is `**Trigger:** <symptom>` / `**Insight:** <root cause + fix>`.
// The Trigger is what a future debugger's query matches, so it must be an
// OBSERVABLE SYMPTOM — what somebody saw. distill used to seed it from the
// commit subject, which is a task description ("feat(x): add Y"), matches
// nothing anyone would type into a search, and is refused outright by the
// knowledge store's own eval exporter.
//
// A symptom cannot be synthesized from a subject: it is frequently nowhere in
// the commit at all. So this file does three honest things, in order:
//
//  1. prefer the author's `symptom:` trailer — the only source that KNOWS;
//  2. otherwise scavenge the commit body for a symptom-shaped observation,
//     deterministically and with precision over recall;
//  3. otherwise refuse to fabricate: emit TriggerTODO so the gap is visible in
//     review, instead of a junk lesson that looks complete.
//
// Deterministic and LLM-free like the rest of the keystone (ADR-0006): the same
// commit yields the same Trigger, byte for byte.

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/trailers"
)

// TriggerTODO is the author-facing placeholder written when neither the
// `symptom:` trailer nor the commit body yields an observable symptom. It is
// exported so review surfaces can flag an un-filled lesson by prefix without
// re-deriving this decision.
const TriggerTODO = "TODO — the observable symptom that led here"

// triggerTODOWhy tells the author which two sources came up empty, so the fix
// is obvious from the rendered lesson alone.
const triggerTODOWhy = " (no `symptom:` trailer, and no observation found in the commit body)"

const (
	// minSymptomWords: below this a candidate is a label, not an observation.
	// Matches the export gate's thin-trigger floor on purpose — distill must not
	// mint a Trigger its own exporter would refuse as too thin.
	minSymptomWords = 8
	// maxSymptomChars: past this a candidate is a paragraph of reasoning, not a
	// symptom. Bounds what lands in a lesson header.
	maxSymptomChars = 400
	// echoThreshold: fraction of one word set contained in the other, at or above
	// which the "symptom" is really the insight restated — the cause, not the
	// observation.
	echoThreshold = 0.60
)

// triggerFor returns the Trigger text for a lesson distilled from c.
func triggerFor(c model.Commit) string {
	if s := strings.TrimSpace(c.Trailer.Symptom); s != "" {
		return firstLine(s) // the author said it outright; take it verbatim
	}
	if s := scavengeSymptom(c.Body, c.Trailer.Learned); s != "" {
		return s
	}
	return TriggerTODO + triggerTODOWhy
}

// scavengeSymptom returns the first symptom-shaped observation in a commit
// body, or "" when the body carries none.
//
// FIRST in document order, deliberately: a commit body opens with the problem
// and closes with the remedy, so the earliest qualifying sentence is the one
// most likely to be an observation rather than a description of the fix.
//
// answer is the commit's `learned:` text; a candidate that restates it is
// rejected — a symptom that contains its own root cause is not a symptom.
func scavengeSymptom(body, answer string) string {
	aw := contentWords(answer)
	for _, u := range observationUnits(body) {
		if !isSymptom(u) {
			continue
		}
		uw := contentWords(u)
		if containment(uw, aw) >= echoThreshold || containment(aw, uw) >= echoThreshold {
			continue // the insight wearing a symptom's clothes
		}
		return u
	}
	return ""
}

// observationUnits splits a commit body into the units a symptom could be: one
// sentence of prose, one list item, or one line of quoted log output. Trailer
// lines, headings and fence markers are structure, never observations.
//
// A unit is a whole THOUGHT, never a physical line: commit bodies are hard
// wrapped at ~72 columns, and scoring a wrapped line on its own both truncates
// the candidate mid-clause and orphans its tail as a fragment that starts
// mid-sentence. Prose is joined into paragraphs; the same treatment extends to
// the two other blocks a wrap can cut — a list item (bullet or numbered, at any
// nesting depth) and a trailer line, whose indented tail is trailer text and so
// is structure, not an observation.
//
// A continuation line is an INDENTED line following the block it continues; an
// indented line carrying its own list marker opens a nested item instead.
func observationUnits(body string) []string {
	var units []string
	var para []string  // the prose paragraph being accumulated
	var item []string  // the list item being accumulated, marker stripped
	itemIndent := 0    // indent of that item's marker; its tail is indented past it
	inTrailer := false // last block opened was a trailer line
	inFence := false

	flushItem := func() {
		if len(item) > 0 {
			units = append(units, splitSentences(strings.Join(item, " "))...)
			item = nil
		}
	}
	flushPara := func() {
		if len(para) > 0 {
			units = append(units, splitSentences(strings.Join(para, " "))...)
			para = nil
		}
	}
	// para and item are never both open, so flushing both preserves document order.
	flush := func() { flushItem(); flushPara() }

	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, "\r")
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
			flush()
			inTrailer, inFence = false, !inFence
		case inFence:
			if t != "" {
				units = append(units, t) // a pasted log line stands alone
			}
		case t == "", strings.HasPrefix(t, "#"):
			flush()
			inTrailer = false
		case trailers.IsTrailerLine(t):
			flush()
			inTrailer = true // whatever is indented under it is still the trailer
		default:
			if li, ok := listItem(t); ok {
				flush()
				inTrailer = false
				item, itemIndent = []string{li}, indentOf(line)
				continue
			}
			if in := indentOf(line); in > 0 {
				if len(item) > 0 && in > itemIndent {
					item = append(item, t) // the rest of this bullet
					continue
				}
				if inTrailer {
					continue // the rest of a wrapped trailer value
				}
			}
			flushItem()
			inTrailer = false
			para = append(para, strings.TrimSpace(strings.TrimPrefix(t, ">")))
		}
	}
	flush()
	return units
}

var listMarkerRE = regexp.MustCompile(`^([-*+]|\d+[.)])\s+`)

// listItem strips a markdown list marker; ok=false when the line is not one.
func listItem(t string) (string, bool) {
	m := listMarkerRE.FindString(t)
	if m == "" {
		return "", false
	}
	return strings.TrimSpace(t[len(m):]), true
}

// indentOf returns a line's leading-whitespace width (tab = 4), the only signal
// separating a wrapped continuation from the next top-level block.
func indentOf(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// splitSentences breaks a paragraph at ".", "!" or "?" followed by whitespace.
// A boundary inside a token ("v0.20.0", "1.5s", "foo.bar") never splits because
// the terminator must be followed by space; an abbreviation ("e.g.", "Fig.")
// never splits because a sentence must carry at least minSentenceWords words.
func splitSentences(p string) []string {
	const minSentenceWords = 3
	var out []string
	r := []rune(p)
	start := 0
	for i := 0; i < len(r); i++ {
		if r[i] != '.' && r[i] != '!' && r[i] != '?' {
			continue
		}
		j := i + 1
		for j < len(r) && strings.ContainsRune(".!?\"'`)", r[j]) {
			j++
		}
		if j < len(r) && !unicode.IsSpace(r[j]) {
			continue
		}
		s := strings.TrimSpace(string(r[start:j]))
		if len(strings.Fields(s)) < minSentenceWords {
			continue
		}
		out = append(out, s)
		start = j
	}
	if s := strings.TrimSpace(string(r[start:])); s != "" {
		out = append(out, s)
	}
	return out
}

// isSymptom is the precision-biased accept test. Everything it accepts must
// also read as a symptom to the store's exporter, so its cues are a subset of
// that gate's vocabulary: a false accept mints a junk lesson forever, while a
// false reject only asks the author for a `symptom:` trailer.
func isSymptom(u string) bool {
	s := strings.TrimSpace(u)
	if s == "" || len(s) > maxSymptomChars {
		return false
	}
	if conventionalSubject.MatchString(s) || prescriptive(s) || describesChange(s) {
		return false
	}
	if cutOff(s) {
		return false
	}
	w := contentWords(s)
	if len(w) < minSymptomWords {
		return false
	}
	return hasSymptomCue(s, w)
}

// conventionalSubject rejects a Conventional Commits subject quoted into the
// body — the exact shape this whole change exists to stop seeding.
var conventionalSubject = regexp.MustCompile(`(?i)^(feat|fix|chore|docs|refactor|perf|test|build|ci|style|revert|wip|merge)(\([^)]*\))?!?:`)

// planLead rejects a sentence that opens by describing work done rather than
// behavior seen. "Removing X dropped Y" survives: the boundary is deliberate.
var planLead = regexp.MustCompile(`(?i)^(add|adds|introduce|introduces|implement|implements|refactor|refactors|rename|renames|move|moves|bump|bumps|wire|wires|switch|switches|make|makes|expose|exposes|extract|extracts|split|splits|document|documents|use|uses|keep|keeps|let|lets|teach|teaches)\b`)

// planPhrase rejects the narrator's voice: a sentence talking about the change
// or the plan is a task description wherever it sits in the body.
var planPhrase = regexp.MustCompile(`(?i)\b(this (commit|change|pr|patch|branch|adr)|we (should|will|now|can|could)|going forward|follow-?up|instead,? (we|this))\b`)

func prescriptive(s string) bool {
	return planLead.MatchString(s) || planPhrase.MatchString(s)
}

// labelHead matches a changelog bullet's leading "<component>:" label — up to
// three identifier-shaped words naming a package, command or file, and the
// colon that introduces what happened to it. Deliberately narrow: a real
// observation may also carry a label ("cache: 4 of 21 lookups returned nothing"),
// so the label alone decides nothing — only what FOLLOWS it does.
var labelHead = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9._/-]*(\s+[a-z0-9][a-z0-9._/-]*){0,2}\s*:\s+`)

// changeHead matches the phrase heads that announce a modification rather than
// a behavior: "new X", "now does Z", "extended to …". planLead covers the
// imperative verbs; these are the shapes it cannot see, because they are not
// verbs ("new") or because they sit behind a label.
var changeHead = regexp.MustCompile(`(?i)^(new|now|extended|extends|extend|dropped|drops|removed|removes|renamed|gains|gained|becomes|became|promoted|promotes|replaces|replaced|folded|folds|reused|reuses|unified|unifies)\b`)

// describesChange rejects a unit that describes the patch instead of what was
// observed — the changelog bullet a commit body is full of. It is the same
// judgement prescriptive() makes, applied where the bullet's "<component>:"
// label hides the verb from a leading-word test, plus the non-verb heads
// ("new …") that announce a change without one.
//
// Precision over recall, per ADR-0028: a bullet that lists what the patch adds
// is never the symptom that made someone write it, and refusing costs only the
// author one `symptom:` line.
func describesChange(s string) bool {
	if changeHead.MatchString(s) {
		return true
	}
	if m := labelHead.FindString(s); m != "" {
		rest := s[len(m):]
		return changeHead.MatchString(rest) || planLead.MatchString(rest)
	}
	return false
}

// danglingTail matches a candidate whose last word is a function word — what a
// clause cut at a line wrap ends on ("… whose dirs the").
var danglingTail = regexp.MustCompile("(?i)(?:^|\\s)(a|an|the|of|to|and|or|that|which|whose|with|for|in|on|at|by|from)[\"'`)\\]]*$")

// unterminatedTail matches a candidate that stops on connective punctuation —
// an unfinished clause promising a continuation that a Trigger will never have.
var unterminatedTail = regexp.MustCompile(`[,;:(\[{–—-]$`)

// cutOff is the structural backstop: a Trigger must never end mid-clause. It is
// independent of how the candidate was assembled, so a wrap this file failed to
// join, a truncation upstream, or a body written by hand in half a sentence all
// land here rather than in the store. A fragment is not a symptom — the TODO
// placeholder is a better outcome than half of one.
//
// Scavenged candidates only. The `symptom:` trailer is taken verbatim (ADR-0028
// second-guesses nothing the author states outright), and an authored sentence
// may legitimately end on a particle — "the subject distill had pasted in".
func cutOff(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || danglingTail.MatchString(s) || unterminatedTail.MatchString(s)
}

// negatedObservation catches symptoms whose cue is the negation itself
// ("the hook did not run") rather than a single failure word.
var negatedObservation = regexp.MustCompile(`(?i)\b(does|did|do|is|are|was|were|has|have|had|would|could|should|will|can)\s+not\b|\bcan'?t\b|\bcannot\b|\bwon'?t\b|\bdoesn'?t\b|\bdidn'?t\b|\bisn'?t\b|\baren'?t\b|\bwasn'?t\b|\bno longer\b|\bnot yet\b`)

// errorArtifact matches the literal evidence an observation quotes: an HTTP or
// exit status in the 4xx–5xx range, or a SCREAMING_SNAKE error constant. Issue
// references and version numbers deliberately do not match — those are how task
// descriptions cite work.
var errorArtifact = regexp.MustCompile(`\b[45]\d{2}\b|\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b`)

// contrastedObservation catches "X happened even though Y" — an author
// reporting the gap between what they saw and what they expected.
var contrastedObservation = regexp.MustCompile(`(?i)\beven though\b|\beven when\b|\bdespite\b|\bbut still\b|\byet still\b`)

func hasSymptomCue(raw string, words []string) bool {
	for _, w := range words {
		if symptomCue[w] {
			return true
		}
	}
	return negatedObservation.MatchString(raw) ||
		errorArtifact.MatchString(raw) ||
		contrastedObservation.MatchString(raw)
}

// symptomCue is the observable-behavior lexicon: failure, wrongness, absence
// and instability. Curated hard — generic verbs and words that carry a design
// meaning in ordinary engineering prose ("never", "refuses", "drops", "empty")
// are excluded, because they wave through task descriptions.
var symptomCue = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		fail fails failed failing failure failures error errors errored
		crash crashes crashed crashloop panic panics panicked hang hangs hung
		timeout timeouts timed deadlock deadlocks stall stalls stalled
		broke broken breakage regress regressed regression
		flaky flake nondeterministic unreproducible
		corrupt corrupted corruption leak leaks leaked leaking exhausted
		wrong incorrect incorrectly unexpected unexpectedly
		mismatch mismatched mismatches inconsistent inconsistency contradicts
		disagree disagrees disagreed diverge diverges diverged divergence
		drift drifts drifted stale staleness duplicate duplicated duplicates
		spurious phantom bogus garbage misleading
		silently silent quietly unnoticed undetected invisible swallowed
		unreachable orphan orphaned dangling zombie
		clobber clobbered overwrote overwritten wiped reaped
		thrash thrashing spike spiked sluggish
		dead die died dying outage storm runaway avalanche
		flap flapping flapped churn churning stuck wedged jammed
		degraded unhealthy unresponsive unavailable offline evicted
		throttled starved saturated backlog pileup
		misread misdiagnosed misattributed notready backoff oomkilled unbound
	`) {
		symptomCue[stem(w)] = true // contentWords stems, so the lexicon must too
	}
}

// containment is |a ∩ b| / |a| — the fraction of a's distinct content words
// that b also carries.
func containment(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(b))
	for _, w := range b {
		set[w] = true
	}
	seen := make(map[string]bool, len(a))
	hit, total := 0, 0
	for _, w := range a {
		if seen[w] {
			continue
		}
		seen[w] = true
		total++
		if set[w] {
			hit++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// contentWords lower-cases, splits on non-alphanumerics, drops stopwords and
// 1–2 character tokens, and stems a trailing plural. This vocabulary is shared
// with the store's eval exporter by construction, not by import — the two live
// on either end of the same pipeline, and folding them into one package once
// both have landed is follow-up work.
func contentWords(s string) []string {
	var out []string
	for _, w := range nonWord.Split(strings.ToLower(s), -1) {
		if len(w) < 3 || stopword[w] {
			continue
		}
		out = append(out, stem(w))
	}
	return out
}

// stem is a deliberately crude plural stripper — enough to make token overlap
// robust, not a linguistics project.
func stem(w string) string {
	if len(w) > 4 && strings.HasSuffix(w, "es") && !strings.HasSuffix(w, "ses") {
		return w[:len(w)-2]
	}
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}
	return w
}

var stopword = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		the and for but not with that this these those from into onto than then
		when what which while who whom whose why how has have had was were are
		its his her their our your there here they them you our ours over under
		out off all any both each few more most other some such only own same
		too very can will just should now via per due about after again against
		because been before being below between during further having once
		other same until above across among around
	`) {
		stopword[w] = true
	}
}
