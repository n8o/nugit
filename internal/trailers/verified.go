package trailers

import (
	"strings"
)

// Verification kinds a `verified:` line may name (ADR-0056). The hook stamps
// e2e, tests and review from session evidence; build, lint and manual are for
// a person writing the line by hand. none is a claim too: it says the commit
// was not verified, and is what makes unverified commits queryable.
const (
	VerifiedE2E    = "e2e"
	VerifiedTests  = "tests"
	VerifiedReview = "review"
	VerifiedBuild  = "build"
	VerifiedLint   = "lint"
	VerifiedManual = "manual"
	VerifiedNone   = "none"
)

var verifiedKinds = map[string]bool{
	VerifiedE2E: true, VerifiedTests: true, VerifiedReview: true, VerifiedBuild: true,
	VerifiedLint: true, VerifiedManual: true, VerifiedNone: true,
}

// IsVerifiedKind reports whether k is one of the verification kinds.
func IsVerifiedKind(k string) bool { return verifiedKinds[k] }

// Verification is a parsed `verified:` value: the kinds it names, in order,
// and the note after them.
//
//	verified: e2e, tests
//	verified: none — docs-only change
//	verified: tests (ran the suite in another terminal)
type Verification struct {
	Kinds []string
	Note  string
}

// ParseVerified reads a `verified:` value. Leading known kinds, separated by
// commas or spaces, are the kinds; the rest, minus a leading dash or
// parenthesis, is the note. A value that starts with no known kind is all
// note. Repeated lines (joined with "; " by Parse) contribute every kind.
func ParseVerified(v string) Verification {
	var out Verification
	var notes []string
	for _, part := range strings.Split(v, "; ") {
		rest := strings.TrimSpace(part)
		for rest != "" {
			tok := rest
			if i := strings.IndexAny(rest, ", \t"); i >= 0 {
				tok = rest[:i]
			}
			k := strings.ToLower(tok)
			if !verifiedKinds[k] {
				break
			}
			if !contains(out.Kinds, k) {
				out.Kinds = append(out.Kinds, k)
			}
			rest = strings.TrimLeft(rest[len(tok):], ", \t")
		}
		rest = strings.TrimSpace(strings.TrimLeft(rest, "—–-:( \t"))
		rest = strings.TrimSpace(strings.TrimSuffix(rest, ")"))
		if rest != "" {
			notes = append(notes, rest)
		}
	}
	out.Note = strings.Join(notes, "; ")
	return out
}

// Verified reports whether the line names a kind of verification that ran:
// any kind except none.
func (v Verification) Verified() bool {
	for _, k := range v.Kinds {
		if k != VerifiedNone {
			return true
		}
	}
	return false
}

// Explained reports whether an unverified line carries a reason: a note, with
// or without `none`. Under verify.gate: enforce a reason lets a commit through
// (ADR-0056).
func (v Verification) Explained() bool { return strings.TrimSpace(v.Note) != "" }

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
