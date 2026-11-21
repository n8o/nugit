package consistency

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// checkDuplicateNumber: a decision this PR adds or modifies shares its file
// number with a decision under a different id, at the reviewed ref. That is
// a renumber done halfway: the id was changed and the file name was not, or
// the other way round. The ids still resolve, but the file a reader opens by
// number is the wrong record, and the next number minted is read from the
// wrong file. When the ids are equal too, checkDuplicateID reports it, and
// DuplicateNumbers leaves that case out so one collision is one finding.
//
// FAIL, for ADR-0039's reason: this is an exact grouping over committed text
// with no false positives to be advisory about, and the fix (rename one file
// or one id) is a one-line edit. Scoped to records the PR touches, so a
// collision fails when it lands and a pre-existing one is doctor's job.
func checkDuplicateNumber(in Input) []model.Finding {
	touched := map[string]bool{}
	for _, kc := range in.Knowledge.Changes {
		if kc.Object != nil && (kc.Status == "A" || kc.Status == "M") {
			touched[kc.Object.Path] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}
	var fs []model.Finding
	for _, d := range knowledge.DuplicateNumbers(in.AllObjects) {
		hit := false
		for _, p := range d.Paths {
			hit = hit || touched[p]
		}
		if !hit {
			continue
		}
		fs = append(fs, model.Finding{
			Check:    "duplicate-decision-number",
			Severity: model.SevFail,
			Title:    fmt.Sprintf("decision number %s is shared by %d records (%s)", d.Number, len(d.Paths), strings.Join(d.IDs, ", ")),
			Detail: d.Describe() + ". A decision's file number and its id are the same key written twice; " +
				"when they disagree, the file a reader opens by number is another record, and the next number minted " +
				"is read from the wrong file. Give each record one number, in its file name and in its `id:`.",
		})
	}
	return fs
}

// checkNumberTakenOnTarget is checkIDTakenOnTarget for decision numbers. The
// branch this PR merges into is read at its TIP (ADR-0041), so a decision this
// PR adds under a number a sibling took after this branch was cut is
// flagged, even though the branch's own history cannot show it. An added
// decision whose id is also taken there is checkIDTakenOnTarget's finding, and
// is left to it.
//
// The target's records come from in.TargetIDs (id to paths). That is every
// local record with an id, and an added decision is only compared with the
// ones in its own directory under its own id namespace.
func checkNumberTakenOnTarget(in Input) []model.Finding {
	if len(in.TargetIDs) == 0 {
		return nil
	}
	type held struct{ id, path string }
	onTarget := map[knowledge.NumberKey][]held{}
	for id, paths := range in.TargetIDs {
		for _, p := range paths {
			if k, ok := knowledge.NumberKeyOf("", id, p); ok {
				onTarget[k] = append(onTarget[k], held{id, p})
			}
		}
	}
	reported := map[knowledge.NumberKey]bool{}
	for _, d := range knowledge.DuplicateNumbers(in.AllObjects) {
		reported[d.NumberKey] = true
	}
	var fs []model.Finding
	for _, kc := range in.Knowledge.Changes {
		o := kc.Object
		if kc.Status != "A" || o == nil || o.Type != model.KindDecision || o.ID == "" || o.Foreign() {
			continue
		}
		k, ok := knowledge.NumberKeyOf("", o.ID, o.Path)
		if !ok || reported[k] {
			continue
		}
		var others []string
		for _, h := range onTarget[k] {
			if h.path != o.Path && h.id != o.ID {
				others = append(others, fmt.Sprintf("%s (%s)", h.path, h.id))
			}
		}
		if len(others) == 0 {
			continue
		}
		reported[k] = true
		fs = append(fs, model.Finding{
			Check:    "duplicate-decision-number",
			Severity: model.SevFail,
			Title:    fmt.Sprintf("decision number %s is already taken on %s", k.Number, in.TargetRef),
			Detail: fmt.Sprintf("this PR adds %s (%s), but %s already holds decision number %s in %s. "+
				"This branch's own history cannot show it: the record landed after the branch was cut, and a "+
				"pull-request check does not re-run when the base moves. Renumber this record to a number free on %s, "+
				"in its file name, its `id:` and every citation of it.",
				o.Path, o.ID, in.TargetRef, k.Number, strings.Join(others, ", "), in.TargetRef),
		})
	}
	return fs
}
