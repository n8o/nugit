package distill

import "strings"

// This file exports the ONE notion of "the store already carries this" that
// distill uses for proposal dedup (ADR-0018), so other writers into a store can
// reuse it instead of inventing a second, subtly different similarity. `nugit
// promote` (ADR-0035) is the first such caller: two near-duplicate records
// arriving in an org hub from two repos is exactly the failure the local dedup
// rule already prevents inside one repo, and a second notion of similarity
// would mean the same pair of lessons is "the same" in one code path and
// "different" in the other.
//
// The rule itself is unchanged and lives in index.dupLesson: an exact
// normalized-text match, or a keyword overlap of ≥2 shared keywords covering ≥
// half the candidate's set.

// Keywords returns a record body's "**Keywords:** a, b" line as a list, empty
// when the body carries none (hand-written records need not have one). This is
// the exact extraction distill indexes on, exported verbatim.
func Keywords(body string) []string { return keywordsText(body) }

// Insight returns a lesson body's "**Insight:** …" text, empty when absent.
func Insight(body string) string { return insightText(body) }

// Section returns the text under a "## Heading" up to the next heading — e.g.
// Section(body, "## Decision").
func Section(body, heading string) string { return sectionText(body, heading) }

// Normalize is the comparison form text is matched in: lowercased, whitespace
// collapsed. Two records whose statements differ only in wrapping or case are
// the same statement.
func Normalize(s string) string { return norm(s) }

// TitleWords is the fallback comparison surface for a record that carries no
// keyword line: the content words of its title. Stop words are dropped so a
// title's overlap is measured on what it is ABOUT, not on how it is phrased.
func TitleWords(title string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.Fields(strings.ToLower(title)) {
		f = strings.Trim(f, ".,:;!?()[]\"'`—–-")
		if len(f) < 3 || titleStopWords[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// titleStopWords are the function words a title shares with every other title;
// counting them as overlap would make unrelated records look alike.
var titleStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
	"from": true, "into": true, "not": true, "but": true, "its": true, "was": true,
	"are": true, "has": true, "had": true, "when": true, "then": true, "than": true,
	"you": true, "our": true, "all": true, "any": true, "one": true, "two": true,
}

// SimilarKeywords reports whether two keyword sets describe the same record
// under the ADR-0018 overlap rule: at least two shared keywords, covering at
// least half of `candidate`. Sets smaller than two keywords never match — the
// rule is deliberately unable to fire on thin evidence.
func SimilarKeywords(candidate, existing []string) bool {
	set := map[string]bool{}
	for _, k := range existing {
		set[norm(k)] = true
	}
	return similarKeywordSet(candidate, set)
}
