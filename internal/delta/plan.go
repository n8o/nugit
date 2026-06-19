package delta

import (
	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
	"gopkg.in/yaml.v3"
)

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
