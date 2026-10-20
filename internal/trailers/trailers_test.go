package trailers

import "testing"

func TestParse(t *testing.T) {
	body := `Charge API can take up to 45s under load.

decision: set timeout to 60s, retry on 429 only
rejected: retry on timeout (masks real failures)
learned: never use library default timeouts for billing endpoints
affects: payments.charge, payments.retry
spec: SPEC-014
keywords: payments, timeout, retry
ticket: JIRA-42`

	tr := Parse(body)
	if tr.Decision == "" || tr.Rejected == "" || tr.Learned == "" {
		t.Errorf("missing typed fields: %+v", tr)
	}
	if len(tr.Affects) != 2 || tr.Affects[0] != "payments.charge" {
		t.Errorf("affects = %+v", tr.Affects)
	}
	if tr.Spec != "SPEC-014" {
		t.Errorf("spec = %q", tr.Spec)
	}
	if len(tr.Keywords) != 3 {
		t.Errorf("keywords = %+v", tr.Keywords)
	}
	if tr.Extra["ticket"] != "JIRA-42" {
		t.Errorf("extra ticket = %+v", tr.Extra)
	}
	if !tr.Has() {
		t.Error("Has() should be true")
	}
}

// ADR-0028: `symptom:` is a typed, OPTIONAL field — it must not fall through to
// Extra, and it must not join the mandatory set.
func TestParseSymptom(t *testing.T) {
	tr := Parse("symptom: checkouts failed with a 504 for ~2% of carts during the evening peak\nlearned: never use library default timeouts\nkeywords: payments")
	if tr.Symptom != "checkouts failed with a 504 for ~2% of carts during the evening peak" {
		t.Errorf("symptom = %q", tr.Symptom)
	}
	if _, dup := tr.Extra["symptom"]; dup {
		t.Errorf("symptom must be typed, not Extra: %+v", tr.Extra)
	}
	if w := Validate(tr); len(w) != 0 {
		t.Errorf("learned+keywords present: want no warnings, got %v", w)
	}
	// mandatory stays mandatory; symptom alone does not satisfy anything
	if w := Validate(Parse("symptom: the poller wedged")); len(w) != 2 {
		t.Errorf("want 2 warnings (learned, keywords), got %v", w)
	}
}

func TestIsTrailerLine(t *testing.T) {
	for _, s := range []string{"learned: x", "  keywords: a, b", "ticket: JIRA-42"} {
		if !IsTrailerLine(s) {
			t.Errorf("IsTrailerLine(%q) = false", s)
		}
	}
	for _, s := range []string{"The endpoint returned 503: the upstream was down", "not a trailer", "Symptom: capitalized is prose", ""} {
		if IsTrailerLine(s) {
			t.Errorf("IsTrailerLine(%q) = true", s)
		}
	}
}

func TestValidate(t *testing.T) {
	// block present but missing learned + keywords
	tr := Parse("decision: do the thing\naffects: x")
	w := Validate(tr)
	if len(w) != 2 {
		t.Errorf("want 2 warnings (learned, keywords), got %v", w)
	}
	// no block at all → no warnings (capture is opt-in)
	if len(Validate(Parse("just a normal commit body"))) != 0 {
		t.Error("absent block should not warn")
	}
}

// ADR-0046: a repeated key keeps every value. Before, the last line won, so a
// commit that made two decisions silently lost the first.
func TestParseRepeatedKeys(t *testing.T) {
	tr := Parse(`symptom: replays could read the solution
decision: clone at base and check the solution object is absent
rejected: a worktree per run — shares objects
rejected: a shallow clone — hides history the agent may use
decision: add oracle and dump arms
learned: assert isolation per run
learned: assert isolation per run
affects: evals
affects: evals, replay
spec: SPEC-001
spec: SPEC-002
keywords: evals, replay
keywords: replay, oracle
ticket: A-1
ticket: A-2`)
	if len(tr.Decisions) != 2 || tr.Decisions[0] != "clone at base and check the solution object is absent" || tr.Decisions[1] != "add oracle and dump arms" {
		t.Errorf("decisions = %q", tr.Decisions)
	}
	if tr.Decision != "clone at base and check the solution object is absent; add oracle and dump arms" {
		t.Errorf("decision = %q", tr.Decision)
	}
	if got := tr.AllDecisions(); len(got) != 2 {
		t.Errorf("AllDecisions = %q", got)
	}
	if tr.Rejected != "a worktree per run — shares objects; a shallow clone — hides history the agent may use" {
		t.Errorf("rejected = %q", tr.Rejected)
	}
	if tr.Learned != "assert isolation per run" {
		t.Errorf("an exact repeat must collapse, learned = %q", tr.Learned)
	}
	if len(tr.Affects) != 2 || tr.Affects[1] != "replay" {
		t.Errorf("affects = %q", tr.Affects)
	}
	if len(tr.Keywords) != 3 || tr.Keywords[2] != "oracle" {
		t.Errorf("keywords = %q", tr.Keywords)
	}
	if tr.Spec != "SPEC-001" {
		t.Errorf("spec is an id, first wins: %q", tr.Spec)
	}
	if tr.Extra["ticket"] != "A-1; A-2" {
		t.Errorf("extra = %q", tr.Extra)
	}
}

// ADR-0048: objective: is typed, first wins, and does not by itself make a
// capture block — a commit that only names its objective needs no learned:.
func TestParseObjective(t *testing.T) {
	tr := Parse("Adds x.\n\nobjective: acme-142\nobjective: acme-9")
	if tr.Objective != "acme-142" {
		t.Errorf("Objective = %q, want acme-142", tr.Objective)
	}
	if tr.Has() || len(tr.Extra) != 0 {
		t.Errorf("objective alone made a capture block: %+v", tr)
	}
	if w := Validate(tr); len(w) != 0 {
		t.Errorf("objective alone warned: %v", w)
	}
}
