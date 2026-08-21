package beads

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// CheckName is the finding check id for every plan-store finding.
const CheckName = "plan-store"

// Lint reports the ways a committed store will render differently from what its
// author meant. Every rule here is a property of nugit's OWN reader — a step
// that silently vanishes, an id that is silently dropped, a status that
// silently becomes "remaining" — which is exactly why it belongs in nugit
// rather than in a per-repo shell script reverse-engineering this package.
//
// Deliberately NOT a schema validator: `bd` owns the schema and will grow
// fields nugit does not read. These checks only fire where the reader's own
// behaviour would surprise someone reading the rendered plan.
func Lint(st Store) []model.Finding {
	var fs []model.Finding
	fs = append(fs, checkPhantomFiles(st)...)
	fs = append(fs, checkDuplicates(st)...)
	fs = append(fs, checkFields(st)...)
	fs = append(fs, checkConcurrentSteps(st)...)
	fs = append(fs, checkPlanSpread(st)...)
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].Title < fs[j].Title })
	return fs
}

// checkPhantomFiles catches the sidecar-log-committed-into-the-store failure.
// nugit globs .beads/**/*.jsonl RECURSIVELY and parses every line as a plan
// step, so `bd`'s own interactions.jsonl / events.jsonl — or an archive/
// subdirectory someone assumed was inert — render as plan steps nobody wrote.
func checkPhantomFiles(st Store) []model.Finding {
	var fs []model.Finding
	for _, f := range st.Files {
		s := st.Stats[f]
		if s.Lines == 0 || s.Skipped == 0 {
			continue
		}
		// A file that contributes nothing, or mostly nothing, is not a plan
		// store. One skipped line among many is a broken step (checkFields
		// covers that); a majority is a file that does not belong here.
		if s.Parsed == 0 {
			fs = append(fs, model.Finding{
				Check: CheckName, Severity: model.SevWarn,
				Title:  fmt.Sprintf("%s is committed under .beads/ but is not a plan store", f),
				Detail: fmt.Sprintf("%d line(s), none of which carry an id or title. nugit globs .beads/**/*.jsonl recursively and parses every line as a plan step, so a `bd` sidecar log (interactions.jsonl, events.jsonl) committed here renders as steps nobody planned. Add it to .beads/.gitignore.", s.Lines),
			})
			continue
		}
		if s.Skipped*2 >= s.Lines {
			fs = append(fs, model.Finding{
				Check: CheckName, Severity: model.SevWarn,
				Title:  fmt.Sprintf("%s: %d of %d line(s) are not plan steps", f, s.Skipped, s.Lines),
				Detail: "Lines nugit cannot read as an issue are skipped silently — an unparseable or key-less line looks exactly like a step that was never written. Either fix the lines or gitignore the file.",
			})
		}
	}
	return fs
}

// checkDuplicates catches the id collision the reader resolves last-write-wins
// and never mentions. Two steps sharing an id means one of them is invisible.
func checkDuplicates(st Store) []model.Finding {
	var fs []model.Finding
	for _, id := range st.DuplicateIDs {
		fs = append(fs, model.Finding{
			Check: CheckName, Severity: model.SevFail,
			Title:  "duplicate plan step id: " + id,
			Detail: "The store carries this id more than once. nugit keeps the LAST occurrence and drops the earlier ones without saying so, so one of these steps — and its status — never renders. Give each step its own id.",
		})
	}
	return fs
}

// checkFields catches steps that render as something other than what they say.
func checkFields(st Store) []model.Finding {
	epics := false
	for _, it := range st.Issues {
		if it.Type == "epic" {
			epics = true
			break
		}
	}
	var noTitle, badStatus, notEpic []string
	for _, it := range st.Issues {
		switch {
		case it.Title == "":
			noTitle = append(noTitle, it.ID)
		case it.ID == "":
			noTitle = append(noTitle, it.Title)
		}
		if !isDone(it.Status) && !isActive(it.Status) && !isOpen(it.Status) {
			badStatus = append(badStatus, fmt.Sprintf("%s=%q", it.ID, it.Status))
		}
		if epics && it.Type != "epic" {
			notEpic = append(notEpic, it.ID)
		}
	}
	var fs []model.Finding
	if len(noTitle) > 0 {
		fs = append(fs, model.Finding{
			Check: CheckName, Severity: model.SevWarn,
			Title:  fmt.Sprintf("%d plan step(s) missing an id or a title", len(noTitle)),
			Detail: "Renders as a bare id (or is dropped entirely when both are absent): " + strings.Join(trunc(noTitle, 8), ", "),
		})
	}
	if len(badStatus) > 0 {
		fs = append(fs, model.Finding{
			Check: CheckName, Severity: model.SevWarn,
			Title:  fmt.Sprintf("%d plan step(s) carry a status nugit does not classify", len(badStatus)),
			Detail: "These render as `remaining` regardless of what they mean — `bd` can emit statuses (deferred, pinned, hooked) that are neither done nor in-flight to this reader: " + strings.Join(trunc(badStatus, 8), ", "),
		})
	}
	if len(notEpic) > 0 {
		fs = append(fs, model.Finding{
			Check: CheckName, Severity: model.SevInfo,
			Title:  fmt.Sprintf("%d issue(s) are not epics and do not render as plan steps", len(notEpic)),
			Detail: "This store uses `epic` as its plan-step type, so everything else collapses into a footnote. That is the right shape for backlog items parked in the store, and the wrong one for a step that was meant to be tracked: " + strings.Join(trunc(notEpic, 8), ", "),
		})
	}
	return fs
}

// checkConcurrentSteps catches a plan with two steps in flight at once. Across
// plans that is normal — it is what concurrent agents look like. WITHIN one
// plan it almost always means an earlier step was started and never closed.
func checkConcurrentSteps(st Store) []model.Finding {
	byPlan := map[string][]string{}
	var order []string
	for _, it := range st.Issues {
		if !isActive(it.Status) {
			continue
		}
		if _, ok := byPlan[it.Plan]; !ok {
			order = append(order, it.Plan)
		}
		byPlan[it.Plan] = append(byPlan[it.Plan], it.ID)
	}
	var fs []model.Finding
	for _, p := range order {
		if len(byPlan[p]) < 2 {
			continue
		}
		fs = append(fs, model.Finding{
			Check: CheckName, Severity: model.SevWarn,
			Title:  fmt.Sprintf("plan %s has %d steps in flight at once", p, len(byPlan[p])),
			Detail: "A plan is a sequence; two live steps in one usually means an earlier step was started and never closed out. " + strings.Join(byPlan[p], ", "),
		})
	}
	return fs
}

// checkPlanSpread catches a sharded store whose file boundaries and id families
// disagree. In a sharded store the FILE decides the plan, so a step whose id
// family says otherwise lands in a plan its author did not name — and, worse,
// a plan split across files is one that two agents will still collide on.
func checkPlanSpread(st Store) []model.Finding {
	if !st.Sharded {
		return nil
	}
	files := map[string]map[string]bool{}
	var order []string
	for _, it := range st.Issues {
		fam := IDFamily(it.ID)
		if fam == "" || fam == it.ID {
			continue // a bd-native id carries no family to disagree with
		}
		if _, ok := files[fam]; !ok {
			order = append(order, fam)
			files[fam] = map[string]bool{}
		}
		files[fam][it.File] = true
	}
	var fs []model.Finding
	for _, fam := range order {
		if len(files[fam]) < 2 {
			continue
		}
		var names []string
		for f := range files[fam] {
			names = append(names, path.Base(f))
		}
		sort.Strings(names)
		fs = append(fs, model.Finding{
			Check: CheckName, Severity: model.SevWarn,
			Title:  fmt.Sprintf("plan %s is split across %d store files", fam, len(names)),
			Detail: "In a sharded store the file names the plan, so these steps render as separate plans — and two agents advancing the same plan still write the same two files. Keep one plan in one file: " + strings.Join(names, ", "),
		})
	}
	return fs
}

func trunc(ss []string, n int) []string {
	if len(ss) <= n {
		return ss
	}
	return append(append([]string{}, ss[:n]...), fmt.Sprintf("… and %d more", len(ss)-n))
}
