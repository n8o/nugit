package consistency

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// checkEdgeVocabulary reports `relates_to` entries nugit cannot act on, scoped
// to the objects this PR adds or modifies.
//
// The reader tolerates anything: ParseEdge splits on the first colon and hands
// back whatever it found, so `ammends:ADR-7` parses, resolves in retrieval's
// one-hop pull, and never amends anything. Same for `supersedes:` written as an
// edge — ADR-0003 derives effective status from the front-matter field of that
// name, so an edge spelling of it looks like a supersession and produces none.
//
// This is the lesson about tolerant readers owing their authors a linter, now
// applied to the knowledge store's edges rather than the plan store's lines
// (ADR-0041). Warn, not fail: an unrecognised verb is still a readable "see
// also", and the store should not need a migration to keep rendering.
func checkEdgeVocabulary(in Input) []model.Finding {
	touched := map[string]bool{}
	for _, kc := range in.Knowledge.Changes {
		if kc.Object != nil && (kc.Status == "A" || kc.Status == "M") {
			touched[kc.Object.Path] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}
	var mine []model.KnowledgeObject
	for _, o := range in.AllObjects {
		if touched[o.Path] {
			mine = append(mine, o)
		}
	}
	st := knowledge.EdgeAudit(mine)
	if len(st.Problems) == 0 {
		return nil
	}
	var unknown, wrongPlace []string
	for _, p := range st.Problems {
		if p.Misplaced != "" {
			wrongPlace = append(wrongPlace, fmt.Sprintf("%s in %s (belongs in %s)", p.Raw, p.Path, p.Misplaced))
			continue
		}
		unknown = append(unknown, fmt.Sprintf("%s in %s", p.Raw, p.Path))
	}
	var fs []model.Finding
	if len(wrongPlace) > 0 {
		fs = append(fs, model.Finding{
			Check: "edge-vocabulary", Severity: model.SevWarn,
			Title: fmt.Sprintf("%d relates_to entry/entries name a front-matter field", len(wrongPlace)),
			Detail: "These do nothing where they are written: " + strings.Join(trunc8(wrongPlace), "; ") +
				". `supersedes:` in particular is where ADR-0003 derives effective status from, so an edge " +
				"spelling of it reads like a supersession and declares none — retrieval keeps serving both records as live.",
		})
	}
	if len(unknown) > 0 {
		fs = append(fs, model.Finding{
			Check: "edge-vocabulary", Severity: model.SevWarn,
			Title: fmt.Sprintf("%d relates_to verb(s) nugit has no rule for", len(unknown)),
			Detail: "These resolve in retrieval's one-hop pull and are consulted by nothing else, so a typo is " +
				"indistinguishable from a working edge: " + strings.Join(trunc8(unknown), "; ") +
				". Known verbs — " + strings.Join(knowledge.KnownRelations(), ", ") +
				". A bare id with no verb is fine and means exactly \"see also\"; use a verb when you mean a lifecycle claim.",
		})
	}
	return fs
}

func trunc8(ss []string) []string {
	if len(ss) <= 8 {
		return ss
	}
	return append(append([]string{}, ss[:8]...), fmt.Sprintf("… and %d more", len(ss)-8))
}
