package skillopt

import (
	"regexp"
	"strings"
)

// The leakage / quality gate. An eval case is worthless — worse, actively
// misleading — if the input reveals the answer, because the model under test
// scores well without diagnosing anything. Three leak vectors, all observed in
// real stores:
//
//  1. the id and title state the conclusion (an id like
//     "LESSON-<subsystem>-reaps-non-protected-<thing>-tags" IS the answer), so
//     `input` is built ONLY from the Trigger section — never the title, id,
//     filename or keywords;
//  2. the Trigger is not a symptom at all but a commit subject or a task
//     description ("feat: add X — setup pre-flight checks");
//  3. the Trigger restates the Insight, so the symptom contains its own cause.
//
// Every signal below is deterministic and biased toward REFUSING: a skipped
// case costs nothing (the store regenerates the corpus next month), a leaky
// case silently inflates the benchmark forever.
const (
	// TierHigh3 is a case that clears every gate with margin: a symptom-shaped
	// trigger rich enough to diagnose from.
	TierHigh3 = "HIGH-3"
	// TierHigh2 is a case that clears every gate but whose trigger is thin —
	// emitted, and labelled so the consumer can score the tiers separately.
	TierHigh2 = "HIGH-2"
	// TierNeedsAgent is a refused object: an agent enrichment pass (ADR-0012,
	// "AI drafts, code enforces") could turn it into a case; this package will not.
	TierNeedsAgent = "NEEDS-AGENT"
)

// Refusal reasons. These are the report's vocabulary, so they are stable strings.
const (
	ReasonStale         = "stale"                  // superseded/invalidated: a dead answer is the wrong gold answer
	ReasonUnratified    = "unratified"             // proposed: a machine draft is not ground truth (ADR-0016)
	ReasonNoTrigger     = "no-trigger"             // nothing to build an input from
	ReasonNoAnswer      = "no-answer"              // no Insight (nor Cause/Fix): nothing to grade against
	ReasonThinTrigger   = "trigger-too-short"      // fewer than minTriggerWords content-bearing words
	ReasonCommitSubject = "trigger-commit-subject" // conventional-commit shape: a task, not a symptom
	ReasonEchoesAnswer  = "trigger-echoes-answer"  // the symptom restates its own cause
	ReasonLeaksTitle    = "trigger-leaks-title"    // the symptom reproduces the answer-bearing title/id
	ReasonNotSymptom    = "trigger-not-a-symptom"  // no observable-behaviour cue anywhere in the trigger
)

const (
	// minTriggerWords: below this a trigger is a label, not a symptom.
	minTriggerWords = 8
	// richTriggerWords: at/above this the trigger carries enough observed state
	// to be diagnosable on its own (HIGH-3) — roughly two clauses of observation.
	richTriggerWords = 14
	// echoThreshold: fraction of the trigger's content words that also appear in
	// the answer. At/above it the symptom is a paraphrase of its own cause.
	echoThreshold = 0.60
	// titleThreshold: fraction of the ANSWER-BEARING title/id words reproduced in
	// the trigger. At/above it the input hands over the conclusion.
	titleThreshold = 0.60
)

// conventionalCommit matches a Conventional Commits subject — the single most
// common non-symptom trigger, because `nugit distill` seeds a lesson's Trigger
// from the commit subject that carried the trailer.
var conventionalCommit = regexp.MustCompile(`(?i)^(feat|fix|chore|docs|refactor|perf|test|build|ci|style|revert|wip|merge)(\([^)]*\))?!?:`)

// symptomCue is the observable-behaviour lexicon: failure, wrongness, absence,
// and negated observation. Deliberately narrow — generic verbs ("returns",
// "shows", "deletes") appear in ordinary design prose and would wave through
// task descriptions.
//
// Calibrated against a real pilot store: an incident trigger names a state
// somebody SAW (a node NotReady, pods dead, a registry flapping), usually
// alongside the literal artifact — an HTTP status, an error constant — that
// made it visible. A task trigger ("ops resilience review (#1184)") names none.
var symptomCue = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		fail fails failed failing failure failures error errors errored
		crash crashes crashed panic panics panicked hang hangs hung
		timeout timeouts timed deadlock deadlocks stall stalls stalled
		broke broken breaks breakage regress regressed regression
		flaky flake nondeterministic unreproducible
		corrupt corrupted corruption leak leaks leaked leaking exhausted
		wrong incorrect incorrectly unexpected unexpectedly surprising
		mismatch mismatched mismatches inconsistent inconsistency contradicts
		disagree disagrees disagreed diverge diverges diverged divergence
		drift drifts drifted stale staleness duplicate duplicated duplicates
		spurious phantom bogus garbage misleading false
		missing absent empty blank zero none nothing lost drop dropped drops
		silently silent quietly unnoticed undetected invisible
		never nowhere skipped skips ignored ignores swallowed
		unreachable orphan orphaned dangling zombie
		clobber clobbered overwrote overwritten wiped reaped
		reject rejects rejected refuse refuses refused
		block blocks blocked deny denies denied
		slow slower sluggish spike spiked thrash thrashing
		nil null undefined nan
		dead die died dying outage storm runaway avalanche
		flap flapping flapped churn churning stuck wedged jammed
		degraded unhealthy unresponsive unavailable offline evicted
		throttled starved saturated backlog pileup
		misread misdiagnosed misattributed
		notready crashloop backoff oomkilled unbound unset unknown
	`) {
		symptomCue[stem(w)] = true // contentWords stems, so the lexicon must too
	}
}

// negatedObservation catches "X does not Y" style symptoms whose cue is the
// negation itself rather than a single word.
var negatedObservation = regexp.MustCompile(`(?i)\b(does|did|do|is|are|was|were|has|have|had|would|could|should|will|can)\s+not\b|\bcan'?t\b|\bcannot\b|\bwon'?t\b|\bdoesn'?t\b|\bdidn'?t\b|\bisn'?t\b|\baren'?t\b|\bwasn'?t\b|\bno longer\b|\bnot yet\b`)

// errorArtifact matches the literal evidence an incident trigger quotes: an
// HTTP/exit status in the 4xx–5xx range, or a SCREAMING_SNAKE error constant
// (NAME_UNKNOWN, OOM_KILL). Issue references (#1184) and versions (v0.20.0)
// deliberately do NOT match — those are how task-shaped triggers cite work.
var errorArtifact = regexp.MustCompile(`\b[45]\d{2}\b|\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b`)

// contrastedObservation catches the shape "X happened even though Y" — an
// author reporting a contradiction between what they saw and what they expected,
// which is a symptom by construction.
var contrastedObservation = regexp.MustCompile(`(?i)\beven though\b|\beven when\b|\bdespite\b|\bbut still\b|\byet still\b|\balthough\b`)

// Verdict is the gate's judgement of one candidate object.
type Verdict struct {
	Tier    string
	Reasons []string
}

// Emitted reports whether the verdict clears the gate at all.
func (v Verdict) Emitted() bool { return v.Tier != TierNeedsAgent }

// candidate is the material one gate decision is made from.
type candidate struct {
	Trigger string // the prospective input
	Answer  string // the prospective gold answer
	// Titleish is the answer-bearing metadata — the title plus the id slug —
	// which the input must not reproduce.
	Titleish string
	// Keywords is the lesson's keyword line: answer-adjacent by construction,
	// never part of the input, and useful for spotting a diagnosis smuggled
	// into the trigger under a phrase the prose itself never uses.
	Keywords string
}

// grade runs every signal over a candidate. All signals run even after the
// first refusal: a report that lists every reason tells the store owner what to
// fix, and one that stops at the first tells them nothing.
func grade(c candidate) Verdict {
	var reasons []string
	switch {
	case strings.TrimSpace(c.Trigger) == "":
		reasons = append(reasons, ReasonNoTrigger)
	default:
		tw := contentWords(c.Trigger)
		if len(tw) < minTriggerWords {
			reasons = append(reasons, ReasonThinTrigger)
		}
		if conventionalCommit.MatchString(strings.TrimSpace(c.Trigger)) {
			reasons = append(reasons, ReasonCommitSubject)
		}
		if !hasSymptomCue(c.Trigger, tw) {
			reasons = append(reasons, ReasonNotSymptom)
		}
		// Whole-trigger echo is scored against the answer ALONE: a symptom
		// legitimately shares topic nouns with the keyword line, so folding
		// keywords in here would refuse good cases wholesale.
		if containment(tw, contentWords(c.Answer)) >= echoThreshold ||
			annotatedWithCause(c.Trigger, contentWords(c.Answer+" "+c.Keywords+" "+c.Titleish)) {
			reasons = append(reasons, ReasonEchoesAnswer)
		}
		if containment(contentWords(c.Titleish), tw) >= titleThreshold {
			reasons = append(reasons, ReasonLeaksTitle)
		}
	}
	if strings.TrimSpace(c.Answer) == "" {
		reasons = append(reasons, ReasonNoAnswer)
	}
	if len(reasons) > 0 {
		return Verdict{Tier: TierNeedsAgent, Reasons: reasons}
	}
	if len(contentWords(c.Trigger)) >= richTriggerWords {
		return Verdict{Tier: TierHigh3}
	}
	return Verdict{Tier: TierHigh2}
}

// trailingParen captures a parenthetical that closes the trigger — the slot
// authors use for the incident citation, e.g. "… pushed (ISSUE#1337)."
var trailingParen = regexp.MustCompile(`\(([^()]*)\)[\s.;:!?]*$`)

// annotatedWithCause catches the compressed form of leak vector 3: the trigger
// reads as a clean symptom, but the author appended their conclusion into the
// citation parenthetical ("… crashed when B had already initialized the device
// (ISSUE#4242, double-init)"). Whole-trigger overlap dilutes a two-word tag to
// nothing, so the closing parenthetical is scored on its own — against every
// answer-bearing token (answer + keywords + title), because such a tag is often
// phrasing the prose itself never uses.
//
// Scoped to the CLOSING parenthetical on purpose: a mid-sentence aside is
// usually an enumeration of observed conditions, and scoring those refuses good
// cases. Groups with fewer than two content words (a bare issue number) are
// ignored — they carry no diagnosis.
func annotatedWithCause(trigger string, answerBearing []string) bool {
	m := trailingParen.FindStringSubmatch(strings.TrimSpace(trigger))
	if m == nil {
		return false
	}
	pw := contentWords(m[1])
	if len(pw) < 2 {
		return false
	}
	return containment(pw, answerBearing) >= echoThreshold
}

// LooksLikeSymptom reports whether s reads as an OBSERVABLE symptom under this
// gate's lexicon — the same judgement `trigger-not-a-symptom` makes, exported so
// other surfaces can ask the question without minting a second, subtly
// different notion of "looks like a symptom" (ADR-0036 reuses it to spot
// symptom-keyed runbooks). It answers only the cue question: length, echo and
// commit-subject checks stay inside grade(), because they are about an eval
// case's inputs, not about whether a sentence describes something someone saw.
func LooksLikeSymptom(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	return hasSymptomCue(s, contentWords(s))
}

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

// containment is |a ∩ b| / |a| — the fraction of a's content words that b also
// carries. Asymmetric on purpose: "does the trigger live inside the answer" and
// "does the trigger reproduce the title" are different questions.
func containment(a, b []string) float64 {
	if len(a) == 0 {
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
// 1–2 character tokens, and strips a trailing plural "s" so "tags"/"tag" match.
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
