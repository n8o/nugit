package verify

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/trailers"
)

// releasedNote is the prefix of the note Decide writes on a released commit.
const releasedNote = "released after"

// View summarizes how the range's commits say they were verified (ADR-0056).
// It is nil when the gate is off, and under observe when no commit carries a
// `verified:` line: a repo that has not installed the hooks sees nothing new.
func View(commits []model.Commit, gate string) *model.VerificationView {
	if gate == Off || len(commits) == 0 {
		return nil
	}
	v := &model.VerificationView{Gate: gate, ByKind: map[string]int{}}
	stamped := 0
	for _, c := range commits {
		if c.Merge {
			continue // no work of its own, and no hook could have stamped it
		}
		v.Commits++
		line := c.Trailer.Verified
		if line != "" {
			stamped++
		}
		p := trailers.ParseVerified(line)
		// A commit the gate released is unverified whatever else its lines
		// claim: the release is the gate saying it found no evidence.
		released := strings.Contains(p.Note, releasedNote)
		if p.Verified() && !released {
			v.Verified++
			for _, k := range p.Kinds {
				if k != trailers.VerifiedNone {
					v.ByKind[k]++
				}
			}
			continue
		}
		v.Unverified = append(v.Unverified, model.UnverifiedCommit{
			SHA: c.SHA, Subject: c.Subject, Note: p.Note, Stamped: line != "", Released: released,
		})
	}
	if v.Commits == 0 || (gate == Observe && stamped == 0) {
		return nil
	}
	if len(v.ByKind) == 0 {
		v.ByKind = nil
	}
	return v
}

// Findings reports, under enforce only, the commits that reached the PR with
// no verification and no reason of their author's: no `verified:` line (made
// outside an agent session or without the hook), a bare `none`, or one the
// gate released after blocking twice. Under observe the view shows them and
// nothing warns, which is what observe means.
func Findings(v *model.VerificationView) []model.Finding {
	if v == nil || v.Gate != Enforce {
		return nil
	}
	var list []string
	for _, u := range v.Unverified {
		if u.Note != "" && !u.Released {
			continue // the author said why
		}
		why := "no `verified:` line"
		switch {
		case u.Released:
			why = "released by the gate after two blocks"
		case u.Stamped:
			why = "`verified: none`, no reason given"
		}
		list = append(list, fmt.Sprintf("`%s` %s (%s)", short(u.SHA), u.Subject, why))
	}
	if len(list) == 0 {
		return nil
	}
	return []model.Finding{{
		Check: "unverified-commits", Severity: model.SevWarn,
		Title: fmt.Sprintf("%d commit(s) reached this PR unverified and unexplained (verify.gate: enforce)", len(list)),
		Detail: strings.Join(list, "; ") + ". Run the repo's verify skill or a review against this branch, or " +
			"say in the PR why these commits needed none (ADR-0056).",
	}}
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
