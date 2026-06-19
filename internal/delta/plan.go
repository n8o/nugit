package delta

import (
	"sort"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
	"gopkg.in/yaml.v3"
)

// DiffPlan reads the plan at base and head and returns the head position
// annotated with what moved (newly completed/started, added/removed items).
func DiffPlan(repo gitutil.Repo, base, head, prefix string) model.PlanPosition {
	h := Plan(repo, head, prefix)
	if !h.Present {
		return h
	}
	b := Plan(repo, base, prefix)
	return diffPlans(b, h)
}

// diffPlans annotates head with what moved relative to base (pure; testable).
func diffPlans(b, h model.PlanPosition) model.PlanPosition {
	if !b.Present {
		return h // nothing to diff against (plan introduced in this PR)
	}
	baseDone := set(b.Completed)
	h.NewlyCompleted = minus(h.Completed, baseDone)
	if h.Current != "" && h.Current != b.Current && !baseDone[h.Current] {
		h.NewlyStarted = []string{h.Current}
	}
	headAll := set(append(append(append([]string{}, h.Completed...), h.Current), h.Remaining...))
	baseAll := set(append(append(append([]string{}, b.Completed...), b.Current), b.Remaining...))
	h.AddedItems = minusSet(headAll, baseAll)
	h.RemovedItems = minusSet(baseAll, headAll)
	return h
}

func set(ss []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range ss {
		if s != "" {
			m[s] = true
		}
	}
	return m
}

func minus(ss []string, exclude map[string]bool) []string {
	var out []string
	for _, s := range ss {
		if s != "" && !exclude[s] {
			out = append(out, s)
		}
	}
	return out
}

func minusSet(a, b map[string]bool) []string {
	var out []string
	for s := range a {
		if !b[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Plan reads the plan-position delta at the reviewed ref (like every other delta):
// a live Beads store if present, else the committed .nugit/plan.yml stand-in, else
// absent. Reading at ref (not the working tree) keeps the report a pure function
// of (base, head).
func Plan(repo gitutil.Repo, ref, prefix string) model.PlanPosition {
	if pos, ok := beads.PlanPosition(repo, ref, prefix); ok {
		return pos
	}
	for _, name := range []string{".nugit/plan.yml", ".nugit/plan.yaml"} {
		if src, err := repo.ShowFile(ref, prefix+name); err == nil && src != "" {
			if pos, ok := parsePlan([]byte(src)); ok {
				return pos
			}
		}
	}
	return model.PlanPosition{Present: false, Note: "no Beads store or .nugit/plan.yml"}
}

// planFile is the minimal committed plan schema (a stand-in until Beads lands).
type planFile struct {
	Completed []string `yaml:"completed"`
	Current   string   `yaml:"current"`
	Remaining []string `yaml:"remaining"`
	Note      string   `yaml:"note"`
}

func parsePlan(b []byte) (model.PlanPosition, bool) {
	var pf planFile
	if err := yaml.Unmarshal(b, &pf); err != nil {
		return model.PlanPosition{}, false
	}
	return model.PlanPosition{
		Present:   true,
		Completed: pf.Completed,
		Current:   pf.Current,
		Remaining: pf.Remaining,
		Note:      pf.Note,
	}, true
}
