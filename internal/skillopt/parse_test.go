package skillopt

import (
	"strings"
	"testing"
)

// The house bold-lead-in form, with a hard-wrapped multi-line section: the
// wrapping is an artifact of the editor, not of the prose, so it is unwrapped.
func TestParseSectionsBoldMultiLine(t *testing.T) {
	body := `# Lesson — something

**Trigger:** the nightly job reported zero rows
for three consecutive runs.

**Insight:** the cursor was advanced before the write committed, so a crash
between the two silently dropped the batch. Advance the cursor after the commit.

**Rejected:** widening the retry window — it hides the ordering bug.

**Keywords:** cursor, batch, ordering
`
	s := ParseSections(body)
	want := "the nightly job reported zero rows for three consecutive runs."
	if s.Trigger != want {
		t.Errorf("Trigger = %q, want %q", s.Trigger, want)
	}
	if got := s.Insight; got == "" || !contains(got, "Advance the cursor after the commit.") {
		t.Errorf("Insight lost its tail: %q", got)
	}
	if contains(s.Insight, "Rejected") || contains(s.Insight, "widening") {
		t.Errorf("Insight bled into the next marker: %q", s.Insight)
	}
	if s.Rejected != "widening the retry window — it hides the ordering bug." {
		t.Errorf("Rejected = %q", s.Rejected)
	}
	if s.Keywords != "cursor, batch, ordering" {
		t.Errorf("Keywords = %q", s.Keywords)
	}
}

// The heading form, which hand-written lessons in real stores use.
func TestParseSectionsHeadingForm(t *testing.T) {
	body := `# Lesson — something

## Trigger

requests to the export endpoint returned 200 with an empty body
after the cache warmed.

## Insight

the warm path never populated the ETag map. Populate it in the warm path.

## Keywords

cache, etag
`
	s := ParseSections(body)
	want := "requests to the export endpoint returned 200 with an empty body after the cache warmed."
	if s.Trigger != want {
		t.Errorf("Trigger = %q, want %q", s.Trigger, want)
	}
	if !contains(s.Insight, "Populate it in the warm path.") {
		t.Errorf("Insight = %q", s.Insight)
	}
	if contains(s.Insight, "cache, etag") {
		t.Errorf("Insight swallowed the next heading's section: %q", s.Insight)
	}
}

// Missing sections are absent, never guessed at — and the object must not
// silently acquire an empty-but-present trigger.
func TestParseSectionsMissing(t *testing.T) {
	s := ParseSections("# Lesson — x\n\nSome free prose with no markers at all.\n")
	if s.Trigger != "" || s.Insight != "" || s.Rejected != "" {
		t.Errorf("markerless body produced sections: %+v", s)
	}
}

// Cause + Fix is the other real spelling of "root cause + fix"; Answer()
// composes them when there is no Insight, and Insight always wins when present.
func TestAnswerFallsBackToCauseFix(t *testing.T) {
	s := ParseSections("**Cause:** the lock was released early.\n\n**Fix:** hold it until the flush returns.\n")
	got := s.Answer()
	if !contains(got, "released early") || !contains(got, "until the flush returns") {
		t.Errorf("Answer() = %q, want cause and fix composed", got)
	}
	s2 := ParseSections("**Insight:** primary.\n\n**Cause:** secondary.\n")
	if s2.Answer() != "primary." {
		t.Errorf("Insight must win over Cause: %q", s2.Answer())
	}
	// "Why" is the same slot as "Cause" — real stores spell it both ways.
	s3 := ParseSections("**Why:** the lease expired mid-flush.\n")
	if s3.Answer() != "the lease expired mid-flush." {
		t.Errorf("Why must fill the Cause slot: %q", s3.Answer())
	}
}

// A restated marker never overwrites the authored one.
func TestFirstMarkerWins(t *testing.T) {
	s := ParseSections("**Trigger:** first.\n\n**Trigger:** second.\n")
	if s.Trigger != "first." {
		t.Errorf("Trigger = %q, want the first occurrence", s.Trigger)
	}
}

// Bold emphasis inside a section's prose is not a lead-in and must not truncate it.
func TestBoldEmphasisInsideProse(t *testing.T) {
	s := ParseSections("**Trigger:** the job **never** finished and the queue grew.\n")
	if !contains(s.Trigger, "queue grew") {
		t.Errorf("mid-prose bold truncated the trigger: %q", s.Trigger)
	}
}

// An UNRECOGNISED lead-in still ends the section: a stray "**Note:**" block must
// never bleed into the input the model under test will see.
func TestUnknownMarkerEndsTheSection(t *testing.T) {
	s := ParseSections("**Trigger:** the export returned an empty body.\n\n**Note:** unrelated aside.\n")
	if s.Trigger != "the export returned an empty body." {
		t.Errorf("Trigger = %q — an unknown lead-in must close the section", s.Trigger)
	}
}

func contains(hay, needle string) bool { return strings.Contains(hay, needle) }
