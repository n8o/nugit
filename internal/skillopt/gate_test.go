package skillopt

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// lesson builds a parsed lesson object the way knowledge.Load would hand one over.
func lesson(id, body string) model.KnowledgeObject {
	return model.KnowledgeObject{
		FrontMatter:     model.FrontMatter{ID: id, Type: model.KindLesson, Scope: "delta", Status: model.StatusActive},
		Path:            ".nugit/lessons/" + strings.ToLower(strings.TrimPrefix(id, "LESSON-")) + ".md",
		Body:            body,
		EffectiveStatus: model.StatusActive,
	}
}

const cleanBody = `# Lesson — advance the cursor only after the write commits

**Trigger:** the nightly export reported zero rows for three consecutive runs
while the upstream table kept growing, and no error was logged anywhere.

**Insight:** the cursor advanced before the write committed, so a crash between
the two silently dropped the whole batch. Advance the cursor after the commit
returns.

**Rejected:** widening the retry window — it hides the ordering bug instead of
fixing it.

**Keywords:** cursor, batch, ordering
`

// A clean lesson emits, and its input is the symptom and nothing else.
func TestCleanLessonEmits(t *testing.T) {
	c, v := Assess(lesson("LESSON-advance-cursor-after-commit", cleanBody))
	if !v.Emitted() {
		t.Fatalf("clean lesson refused: %v", v.Reasons)
	}
	if v.Tier != TierHigh3 {
		t.Errorf("tier = %s, want %s for a rich symptom", v.Tier, TierHigh3)
	}
	if !strings.HasPrefix(c.Input, "the nightly export reported zero rows") {
		t.Errorf("input = %q, want the trigger text", c.Input)
	}
	if !strings.Contains(c.GoldAnswer, "Advance the cursor after the commit returns.") {
		t.Errorf("gold_answer lost the fix: %q", c.GoldAnswer)
	}
	if !strings.Contains(c.GoldAnswer, "Do not: widening the retry window") {
		t.Errorf("gold_answer lost the Rejected negative guidance: %q", c.GoldAnswer)
	}
	if c.Number != "lesson:advance-cursor-after-commit" {
		t.Errorf("number = %q", c.Number)
	}
	if c.Source != "lessons/advance-cursor-after-commit.md" {
		t.Errorf("source = %q, want the .nugit-relative path", c.Source)
	}
}

// LEAK VECTOR 1: the id and title state the conclusion, so neither may appear in
// the input. This is the property the whole export rests on.
func TestInputNeverCarriesTitleOrID(t *testing.T) {
	c, v := Assess(lesson("LESSON-advance-cursor-after-commit", cleanBody))
	if !v.Emitted() {
		t.Fatalf("clean lesson refused: %v", v.Reasons)
	}
	if c.Title == "" {
		t.Fatal("title metadata missing — the harness still needs it")
	}
	for _, banned := range []string{c.Title, c.Number, "LESSON-", "advance-cursor-after-commit", "lessons/"} {
		if strings.Contains(strings.ToLower(c.Input), strings.ToLower(banned)) {
			t.Errorf("input leaks answer-bearing metadata %q: %q", banned, c.Input)
		}
	}
	// The keywords are answer-adjacent too and are never part of the input.
	if strings.Contains(c.Input, "cursor") {
		t.Errorf("input leaked the keyword/answer token 'cursor': %q", c.Input)
	}
}

// LEAK VECTOR 2: a Conventional Commits subject is a task description, not an
// observable symptom. `nugit distill` seeds Trigger from the commit subject, so
// this is the single most common junk case in a real store.
func TestCommitSubjectTriggerRefused(t *testing.T) {
	body := `# Lesson — dogfooding a health check finds real gaps

**Trigger:** feat: nugit doctor — setup pre-flight health checks

**Insight:** dogfooding a health-check finds real gaps; the repo was missing its
own commit-msg hook. Run the check against your own repo before shipping it.

**Keywords:** doctor, pre-flight
`
	_, v := Assess(lesson("LESSON-dogfooding-finds-real-gaps", body))
	if v.Emitted() {
		t.Fatalf("a commit-subject trigger must be refused, got tier %s", v.Tier)
	}
	if !hasReason(v, ReasonCommitSubject) {
		t.Errorf("reasons = %v, want %s", v.Reasons, ReasonCommitSubject)
	}
}

// LEAK VECTOR 3: a trigger that restates the insight hands the model its own
// answer — it would score well having diagnosed nothing.
func TestTriggerEchoingInsightRefused(t *testing.T) {
	body := `# Lesson — flush before advancing the offset

**Trigger:** the consumer advanced its offset before the flush completed, so a
crash silently dropped buffered records that were never replayed.

**Insight:** the consumer advanced its offset before the flush completed, so a
crash silently dropped buffered records that were never replayed. Advance the
offset only after the flush returns.
`
	_, v := Assess(lesson("LESSON-flush-before-advancing-offset", body))
	if v.Emitted() {
		t.Fatalf("a trigger echoing the insight must be refused, got tier %s", v.Tier)
	}
	if !hasReason(v, ReasonEchoesAnswer) {
		t.Errorf("reasons = %v, want %s", v.Reasons, ReasonEchoesAnswer)
	}
}

// The compressed form of the same leak: a clean symptom with the author's
// conclusion appended into the citation parenthetical. Whole-trigger overlap
// dilutes a two-word tag to nothing, so the closing parenthetical is scored on
// its own against every answer-bearing token — including the keyword line, where
// such a tag is often the only place the phrasing appears.
func TestTriggerAnnotatedWithCauseRefused(t *testing.T) {
	body := `# Lesson — the runtime inits once per process

**Trigger:** the transmitter crashed when a receiver had already initialized the
device, and nothing in the logs pointed at either one (ISSUE#4242, runtime double-init).

**Insight:** resources that init once per process reject the second caller. Share
one process-singleton handle between them.

**Keywords:** runtime, double-init, process-singleton
`
	_, v := Assess(lesson("LESSON-runtime-inits-once-per-process", body))
	if v.Emitted() {
		t.Fatalf("a trigger annotated with its own cause must be refused, got tier %s", v.Tier)
	}
	if !hasReason(v, ReasonEchoesAnswer) {
		t.Errorf("reasons = %v, want %s", v.Reasons, ReasonEchoesAnswer)
	}
}

// …but a bare citation parenthetical is not a diagnosis and must not refuse an
// otherwise clean case.
func TestBareCitationParentheticalIsFine(t *testing.T) {
	body := strings.Replace(cleanBody, "no error was logged anywhere.",
		"no error was logged anywhere (ISSUE#1337).", 1)
	_, v := Assess(lesson("LESSON-advance-cursor-after-commit", body))
	if !v.Emitted() {
		t.Errorf("a bare issue citation must not refuse the case: %v", v.Reasons)
	}
}

// A trigger that reproduces the answer-bearing title is the same leak wearing a
// different hat.
func TestTriggerLeakingTitleRefused(t *testing.T) {
	body := `# Lesson — the reaper deletes non-protected image tags

**Trigger:** the reaper deletes non-protected image tags and the cache misses
spike whenever it runs on a weekday morning.

**Insight:** the retention policy treats an unprotected tag as garbage after 24h.
Mark release tags protected so the reaper skips them.
`
	_, v := Assess(lesson("LESSON-reaper-deletes-non-protected-image-tags", body))
	if v.Emitted() {
		t.Fatalf("a trigger reproducing the title must be refused, got tier %s", v.Tier)
	}
	if !hasReason(v, ReasonLeaksTitle) {
		t.Errorf("reasons = %v, want %s", v.Reasons, ReasonLeaksTitle)
	}
}

// A trigger with no observable-behaviour cue is a situation, not a symptom.
func TestNonSymptomTriggerRefused(t *testing.T) {
	body := `# Lesson — read every artifact from the reviewed ref

**Trigger:** computing a delta or a check that compares the code against the
model inside a pull-request-time continuous integration tool.

**Insight:** read every artifact from the reviewed git ref, never from the
working tree, or the graph disagrees with the diff it claims to describe.
`
	_, v := Assess(lesson("LESSON-read-from-reviewed-ref", body))
	if v.Emitted() {
		t.Fatalf("a task-shaped trigger must be refused, got tier %s", v.Tier)
	}
	if !hasReason(v, ReasonNotSymptom) {
		t.Errorf("reasons = %v, want %s", v.Reasons, ReasonNotSymptom)
	}
}

// A one-clause trigger is a label, not a diagnosable symptom.
func TestThinTriggerRefused(t *testing.T) {
	body := "**Trigger:** the build failed.\n\n**Insight:** a transitive dependency " +
		"pinned an incompatible minor version; pin it explicitly in the lockfile.\n"
	_, v := Assess(lesson("LESSON-build-failed", body))
	if v.Emitted() {
		t.Fatalf("a one-clause trigger must be refused, got tier %s", v.Tier)
	}
	if !hasReason(v, ReasonThinTrigger) {
		t.Errorf("reasons = %v, want %s", v.Reasons, ReasonThinTrigger)
	}
}

// No trigger / no answer: nothing to build a case from, and the report says so.
func TestMissingSectionsRefused(t *testing.T) {
	_, v := Assess(lesson("LESSON-x", "**Insight:** something true but unprompted.\n"))
	if !hasReason(v, ReasonNoTrigger) {
		t.Errorf("reasons = %v, want %s", v.Reasons, ReasonNoTrigger)
	}
	_, v2 := Assess(lesson("LESSON-y", "**Trigger:** the exporter emitted zero rows for three consecutive nightly runs.\n"))
	if !hasReason(v2, ReasonNoAnswer) {
		t.Errorf("reasons = %v, want %s", v2.Reasons, ReasonNoAnswer)
	}
}

// Every failing signal is reported, not just the first — the report is what
// tells a store owner which lessons to rewrite.
func TestAllReasonsReported(t *testing.T) {
	_, v := Assess(lesson("LESSON-z", "**Trigger:** feat: add the thing\n"))
	for _, want := range []string{ReasonThinTrigger, ReasonCommitSubject, ReasonNotSymptom, ReasonNoAnswer} {
		if !hasReason(v, want) {
			t.Errorf("reasons = %v, missing %s", v.Reasons, want)
		}
	}
}

func hasReason(v Verdict, want string) bool {
	for _, r := range v.Reasons {
		if r == want {
			return true
		}
	}
	return false
}
