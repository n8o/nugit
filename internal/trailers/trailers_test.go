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
