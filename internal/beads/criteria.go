package beads

import (
	"regexp"
	"strconv"
	"strings"
)

// Criterion is one numbered acceptance criterion of a bead (ADR-0066): a line
// of its acceptance_criteria that starts with `AC-<n>:`. Its id within the
// bead is "AC-<n>" and never changes, so a commit's `meets:` stays true after
// the bead is edited.
type Criterion struct {
	N    int
	Text string
}

// ID is the criterion's id within its bead, "AC-<n>".
func (c Criterion) ID() string { return "AC-" + strconv.Itoa(c.N) }

var criterionLine = regexp.MustCompile(`^(?:[-*][ \t]+)?AC-([0-9]+):[ \t]*(.*)$`)

// Criteria returns the numbered criteria in an acceptance_criteria value, in
// the order written. A value with no `AC-<n>:` line has none: numbering is
// opt-in per bead. A number written twice keeps its first line.
func Criteria(acceptance string) []Criterion {
	var out []Criterion
	seen := map[int]bool{}
	for _, raw := range strings.Split(acceptance, "\n") {
		m := criterionLine.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, Criterion{N: n, Text: strings.TrimSpace(m[2])})
	}
	return out
}

// CriterionID reads one `meets:` entry: "AC-3", "ac-3" and "AC-03" are
// AC-3, and "<bead-id>/AC-3" is AC-3 of that bead (bead is "" when the entry
// names none). ok is false for anything else.
func CriterionID(s string) (bead, id string, ok bool) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		bead, s = s[:i], s[i+1:]
	}
	if len(s) < 4 || !strings.EqualFold(s[:3], "AC-") {
		return "", "", false
	}
	n, err := strconv.Atoi(s[3:])
	if err != nil || n <= 0 {
		return "", "", false
	}
	return bead, "AC-" + strconv.Itoa(n), true
}
