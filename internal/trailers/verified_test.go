package trailers

import (
	"reflect"
	"testing"
)

func TestParseVerified(t *testing.T) {
	for _, c := range []struct {
		in       string
		kinds    []string
		note     string
		verified bool
	}{
		{"e2e, tests", []string{"e2e", "tests"}, "", true},
		{"e2e tests review", []string{"e2e", "tests", "review"}, "", true},
		{"none — docs-only change", []string{"none"}, "docs-only change", false},
		{"none - typo fix", []string{"none"}, "typo fix", false},
		{"tests (ran the suite in another terminal)", []string{"tests"}, "ran the suite in another terminal", true},
		{"none", []string{"none"}, "", false},
		{"ran it by hand", nil, "ran it by hand", false},
		{"E2E", []string{"e2e"}, "", true},
		{"review; none — gate released after 2 blocks", []string{"review", "none"}, "gate released after 2 blocks", true},
	} {
		v := ParseVerified(c.in)
		if !reflect.DeepEqual(v.Kinds, c.kinds) || v.Note != c.note || v.Verified() != c.verified {
			t.Errorf("%q: got kinds=%v note=%q verified=%v, want %v %q %v",
				c.in, v.Kinds, v.Note, v.Verified(), c.kinds, c.note, c.verified)
		}
	}
}

// verified: is not capture. A commit carrying only it (and objective:) must
// not trip capture-hygiene's "trailer block present but missing learned:".
func TestVerifiedIsNotACaptureBlock(t *testing.T) {
	tr := Parse("body\n\nverified: e2e, tests\nobjective: VERIFY-2\n")
	if tr.Verified != "e2e, tests" {
		t.Fatalf("Verified = %q", tr.Verified)
	}
	if tr.Has() {
		t.Errorf("a verified: line alone is not a capture block: %+v", tr)
	}
	if w := Validate(tr); len(w) != 0 {
		t.Errorf("no capture warnings for verified: alone: %v", w)
	}
}
