package beads

import (
	"regexp"
	"strings"
)

// clarification is spec-kit's open-question marker, which a bead carries
// until the question is answered in the store (ADR-0066).
var clarification = regexp.MustCompile(`\[NEEDS CLARIFICATION:\s*([^\]]*)\]`)

// OpenQuestions returns the `[NEEDS CLARIFICATION: …]` questions in a bead's
// title, description and acceptance criteria, in that order, each once. A
// marker with no text is listed as "(no question written)", so it still
// blocks.
func OpenQuestions(it Issue) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range []string{it.Title, it.Field("description", "body"), it.Field("acceptance_criteria", "acceptance")} {
		for _, m := range clarification.FindAllStringSubmatch(s, -1) {
			q := strings.Join(strings.Fields(m[1]), " ")
			if q == "" {
				q = "(no question written)"
			}
			if !seen[q] {
				seen[q] = true
				out = append(out, q)
			}
		}
	}
	return out
}
