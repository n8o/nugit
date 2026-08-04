package distill

// Round-trip guard: a lesson this package writes must survive the knowledge
// store's own eval exporter (ADR-0027, `nugit export -format skillopt`), whose
// leakage gate refuses a Trigger that is a commit subject
// (`trigger-commit-subject`) or that names no observable behavior
// (`trigger-not-a-symptom`). Before ADR-0028 distill seeded Trigger from the
// commit subject, so its own output tripped both signals — the capture pipeline
// produced lessons the exporter would not export.
//
// The gate's signals are REPLICATED below rather than imported: the exporter
// lives on its own branch, and this change must build and merge against main
// alone. The replica is a verbatim transcription of that gate — if the two ever
// drift, the vocabulary-parity tests at the bottom of this file are the tripwire.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	gateReasonNoTrigger     = "no-trigger"
	gateReasonNoAnswer      = "no-answer"
	gateReasonThinTrigger   = "trigger-too-short"
	gateReasonCommitSubject = "trigger-commit-subject"
	gateReasonEchoesAnswer  = "trigger-echoes-answer"
	gateReasonLeaksTitle    = "trigger-leaks-title"
	gateReasonNotSymptom    = "trigger-not-a-symptom"
)

const (
	gateMinTriggerWords = 8
	gateEchoThreshold   = 0.60
	gateTitleThreshold  = 0.60
)

// A distilled lesson passes the export gate's trigger checks — the claim this
// whole change exists to make true.
func TestDistilledLessonPassesExportGate(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")[:40]

	// (1) the author filled in `symptom:`.
	authored := "fix(retrieval): rank by scope before truncating"
	os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", strings.Join([]string{
		authored,
		"",
		"symptom: agents asking for context on an api file got global lessons first while the component's own ADR was silently dropped at the budget line",
		"decision: order by nearest scope, then truncate",
		"learned: rank by nearest scope before applying the truncation budget",
		"affects: retrieval",
		"keywords: retrieval, budget, scope",
	}, "\n"))

	// (2) no `symptom:` trailer — the observation is scavenged from the body.
	scavenged := "fix(mapping): resolve globs from the reviewed ref"
	os.WriteFile(filepath.Join(dir, "f"), []byte("c"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", strings.Join([]string{
		scavenged,
		"",
		"Two agents produced different component bundles from the identical store",
		"even though both runs read the same reviewed ref.",
		"",
		"decision: resolve every glob against the reviewed ref",
		"learned: resolve path globs against the reviewed ref, never the working tree",
		"affects: mapping",
		"keywords: mapping, globs, determinism",
	}, "\n"))

	res, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Now: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lessons) != 2 {
		t.Fatalf("want 2 lessons, got %v", res.Lessons)
	}

	subjects := []string{authored, scavenged}
	for i, rel := range res.Lessons {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		c := gateCandidateOf(string(b))
		if reasons := gateGrade(c); len(reasons) != 0 {
			t.Errorf("%s: export gate refused a distilled lesson: %v\ntrigger: %q", rel, reasons, c.trigger)
		}
		// and the old behavior, on the same lesson, would have been refused for
		// exactly the two signals ADR-0028 names.
		before := c
		before.trigger = subjects[i]
		reasons := gateGrade(before)
		for _, want := range []string{gateReasonCommitSubject, gateReasonNotSymptom} {
			if !contains(reasons, want) {
				t.Errorf("%s: control (subject-seeded trigger) should be refused for %s; got %v", rel, want, reasons)
			}
		}
	}
}

// The TODO placeholder is deliberately NOT exportable: an un-filled lesson must
// be refused by the gate (and fixed by a human), never quietly turned into an
// eval case.
func TestPlaceholderTriggerIsRefusedByExportGate(t *testing.T) {
	c := gateCandidate{
		trigger:  TriggerTODO + triggerTODOWhy,
		answer:   "dogfooding a health-check finds real gaps",
		titleish: "dogfooding a health-check finds real gaps",
	}
	if reasons := gateGrade(c); len(reasons) == 0 {
		t.Error("the TODO placeholder must not clear the export gate")
	}
}

// --- vocabulary parity with the exporter's gate ---

// Every cue distill accepts must also read as a symptom to the exporter,
// otherwise distill mints triggers its own gate refuses as `trigger-not-a-symptom`.
func TestSymptomCueIsSubsetOfGateLexicon(t *testing.T) {
	for w := range symptomCue {
		if !gateSymptomCue[w] {
			t.Errorf("distill cue %q is not in the export gate's lexicon: a scavenged trigger using it would be refused", w)
		}
	}
}

// The thin-trigger floor is counted in content words, so the two stopword sets
// must agree or distill's word count would not predict the gate's.
func TestContentWordVocabularyMatchesGate(t *testing.T) {
	for w := range stopword {
		if !gateStopword[w] {
			t.Errorf("stopword %q is unknown to the export gate", w)
		}
	}
	for w := range gateStopword {
		if !stopword[w] {
			t.Errorf("export-gate stopword %q is unknown to distill", w)
		}
	}
	if minSymptomWords != gateMinTriggerWords {
		t.Errorf("minSymptomWords = %d, gate floor = %d", minSymptomWords, gateMinTriggerWords)
	}
}

// --- replica of the ADR-0027 leakage gate ---

type gateCandidate struct{ trigger, answer, titleish, keywords string }

// gateCandidateOf reads a distilled lesson file the way the exporter does:
// input from the Trigger section ONLY, answer from Insight, titleish from the
// heading and the id.
func gateCandidateOf(body string) gateCandidate {
	var c gateCandidate
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "**Trigger:**"):
			c.trigger = strings.TrimSpace(strings.TrimPrefix(t, "**Trigger:**"))
		case strings.HasPrefix(t, "**Insight:**"):
			c.answer = strings.TrimSpace(strings.TrimPrefix(t, "**Insight:**"))
		case strings.HasPrefix(t, "**Keywords:**"):
			c.keywords = strings.TrimSpace(strings.TrimPrefix(t, "**Keywords:**"))
		case strings.HasPrefix(t, "# Lesson — "):
			c.titleish = strings.TrimSpace(strings.TrimPrefix(t, "# Lesson — "))
		case strings.HasPrefix(t, "id: "):
			c.titleish += " " + strings.ReplaceAll(strings.TrimPrefix(t, "id: "), "-", " ")
		}
	}
	return c
}

func gateGrade(c gateCandidate) []string {
	var reasons []string
	if strings.TrimSpace(c.trigger) == "" {
		reasons = append(reasons, gateReasonNoTrigger)
	} else {
		tw := gateContentWords(c.trigger)
		if len(tw) < gateMinTriggerWords {
			reasons = append(reasons, gateReasonThinTrigger)
		}
		if gateConventional.MatchString(strings.TrimSpace(c.trigger)) {
			reasons = append(reasons, gateReasonCommitSubject)
		}
		if !gateHasCue(c.trigger, tw) {
			reasons = append(reasons, gateReasonNotSymptom)
		}
		if gateContainment(tw, gateContentWords(c.answer)) >= gateEchoThreshold ||
			gateAnnotated(c.trigger, gateContentWords(c.answer+" "+c.keywords+" "+c.titleish)) {
			reasons = append(reasons, gateReasonEchoesAnswer)
		}
		if gateContainment(gateContentWords(c.titleish), tw) >= gateTitleThreshold {
			reasons = append(reasons, gateReasonLeaksTitle)
		}
	}
	if strings.TrimSpace(c.answer) == "" {
		reasons = append(reasons, gateReasonNoAnswer)
	}
	return reasons
}

var gateTrailingParen = regexp.MustCompile(`\(([^()]*)\)[\s.;:!?]*$`)

func gateAnnotated(trigger string, answerBearing []string) bool {
	m := gateTrailingParen.FindStringSubmatch(strings.TrimSpace(trigger))
	if m == nil {
		return false
	}
	pw := gateContentWords(m[1])
	if len(pw) < 2 {
		return false
	}
	return gateContainment(pw, answerBearing) >= gateEchoThreshold
}

var (
	gateConventional = regexp.MustCompile(`(?i)^(feat|fix|chore|docs|refactor|perf|test|build|ci|style|revert|wip|merge)(\([^)]*\))?!?:`)
	gateNegated      = regexp.MustCompile(`(?i)\b(does|did|do|is|are|was|were|has|have|had|would|could|should|will|can)\s+not\b|\bcan'?t\b|\bcannot\b|\bwon'?t\b|\bdoesn'?t\b|\bdidn'?t\b|\bisn'?t\b|\baren'?t\b|\bwasn'?t\b|\bno longer\b|\bnot yet\b`)
	gateArtifact     = regexp.MustCompile(`\b[45]\d{2}\b|\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b`)
	gateContrasted   = regexp.MustCompile(`(?i)\beven though\b|\beven when\b|\bdespite\b|\bbut still\b|\byet still\b|\balthough\b`)
	gateNonWord      = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

func gateHasCue(raw string, words []string) bool {
	for _, w := range words {
		if gateSymptomCue[w] {
			return true
		}
	}
	return gateNegated.MatchString(raw) || gateArtifact.MatchString(raw) || gateContrasted.MatchString(raw)
}

func gateContainment(a, b []string) float64 {
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

func gateContentWords(s string) []string {
	var out []string
	for _, w := range gateNonWord.Split(strings.ToLower(s), -1) {
		if len(w) < 3 || gateStopword[w] {
			continue
		}
		out = append(out, gateStem(w))
	}
	return out
}

func gateStem(w string) string {
	if len(w) > 4 && strings.HasSuffix(w, "es") && !strings.HasSuffix(w, "ses") {
		return w[:len(w)-2]
	}
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}
	return w
}

var gateSymptomCue = map[string]bool{}

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
		gateSymptomCue[gateStem(w)] = true
	}
}

var gateStopword = map[string]bool{}

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
		gateStopword[w] = true
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
