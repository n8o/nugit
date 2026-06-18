// Package trailers parses the nugit commit-trailer convention (§6.1):
//
//	fix(payments): handle slow charge endpoint timeout
//
//	Charge API can take up to 45s under load; default 10s causes false failures.
//
//	decision: set timeout to 60s, retry on 429 only
//	rejected: retry on timeout (masks real failures, inflates cost)
//	learned: never use library default timeouts for billing endpoints
//	affects: payments.charge
//	spec: SPEC-014
//	keywords: payments, timeout, retry, billing
package trailers

import (
	"regexp"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// trailerLine matches "key: value" where key is a lowercase token. Conventional
// Commit subjects ("fix(x): ...") are excluded because we only scan the body.
var trailerLine = regexp.MustCompile(`^([a-z][a-z0-9_-]*):[ \t]+(.*)$`)

// Parse extracts a Trailer from a commit body. Recognized keys are mapped onto
// typed fields; any other "key: value" line is preserved in Extra.
func Parse(body string) model.Trailer {
	t := model.Trailer{Extra: map[string]string{}}
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, "\r")
		m := trailerLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		key, val := m[1], strings.TrimSpace(m[2])
		switch key {
		case "decision":
			t.Decision = val
		case "rejected":
			t.Rejected = val
		case "learned":
			t.Learned = val
		case "spec":
			t.Spec = val
		case "affects":
			t.Affects = splitList(val)
		case "keywords":
			t.Keywords = splitList(val)
		default:
			t.Extra[key] = val
		}
	}
	if len(t.Extra) == 0 {
		t.Extra = nil
	}
	return t
}

// splitList splits a comma- or space-separated list and trims empties.
func splitList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' })
	var out []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Validate reports problems with a trailer block per the mandatory-field rule
// (§6.1: learned: and keywords: are mandatory when a trailer block is present).
// It returns human-readable warnings; an empty slice means the block is clean.
// An absent block (Has()==false) is not a violation — capture is opt-in.
func Validate(t model.Trailer) []string {
	if !t.Has() {
		return nil
	}
	var warns []string
	if strings.TrimSpace(t.Learned) == "" {
		warns = append(warns, "trailer block present but missing mandatory `learned:`")
	}
	if len(t.Keywords) == 0 {
		warns = append(warns, "trailer block present but missing mandatory `keywords:`")
	}
	return warns
}
