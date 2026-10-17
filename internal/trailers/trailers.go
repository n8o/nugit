// Package trailers parses the nugit commit-trailer convention (§6.1):
//
//	fix(payments): handle slow charge endpoint timeout
//
//	Charge API can take up to 45s under load; default 10s causes false failures.
//
//	symptom: checkouts failed with a 504 for ~2% of carts during the evening peak
//	decision: set timeout to 60s, retry on 429 only
//	rejected: retry on timeout (masks real failures, inflates cost)
//	learned: never use library default timeouts for billing endpoints
//	affects: payments.charge
//	spec: SPEC-014
//	keywords: payments, timeout, retry, billing
//
// `learned:` and `keywords:` are the only mandatory fields; `symptom:` is
// optional but is the sole reliable seed for a distilled lesson's Trigger
// (ADR-0028) — nothing else in a commit records what was OBSERVED.
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
//
// A key may repeat — a commit that makes two decisions, or a squash-merge body
// carrying several commits' blocks. Nothing is dropped (ADR-0046): every
// `decision:` lands in Decisions (and Decision joins them), the other prose
// fields join their values with "; ", and affects/keywords take the ordered
// union. spec: is an id, not prose, so the first one wins.
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
			if val != "" {
				t.Decisions = append(t.Decisions, val)
			}
		case "rejected":
			t.Rejected = join(t.Rejected, val)
		case "learned":
			t.Learned = join(t.Learned, val)
		case "symptom":
			t.Symptom = join(t.Symptom, val)
		case "spec":
			if t.Spec == "" {
				t.Spec = val
			}
		case "affects":
			t.Affects = union(t.Affects, splitList(val))
		case "keywords":
			t.Keywords = union(t.Keywords, splitList(val))
		default:
			t.Extra[key] = join(t.Extra[key], val)
		}
	}
	t.Decision = strings.Join(t.Decisions, "; ")
	if len(t.Extra) == 0 {
		t.Extra = nil
	}
	return t
}

// IsTrailerLine reports whether a line would be consumed by Parse as a
// "key: value" trailer. Callers that scan a commit body for prose (ADR-0028's
// symptom scavenger) use it to exclude the capture block, so the convention
// lives in exactly one place.
func IsTrailerLine(line string) bool {
	return trailerLine.MatchString(strings.TrimSpace(strings.TrimRight(line, "\r")))
}

// splitList splits a comma- or space-separated list and trims empties.
// join appends a repeated key's value to what is already held, skipping an
// empty value and an exact repeat (the same line in two squashed commits).
func join(have, val string) string {
	switch {
	case val == "" || val == have:
		return have
	case have == "":
		return val
	}
	for _, p := range strings.Split(have, "; ") {
		if p == val {
			return have
		}
	}
	return have + "; " + val
}

// union appends the items of add not already in have, preserving order.
func union(have, add []string) []string {
	for _, a := range add {
		dup := false
		for _, h := range have {
			if h == a {
				dup = true
				break
			}
		}
		if !dup {
			have = append(have, a)
		}
	}
	return have
}

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
