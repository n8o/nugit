package delta

import (
	"os"
	"path/filepath"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/model"
	"gopkg.in/yaml.v3"
)

// Plan reads the plan-position delta: a live Beads store if present, else the
// committed .nugit/plan.yml stand-in, else absent.
func Plan(repoDir string) model.PlanPosition {
	if pos, ok := beads.PlanPosition(repoDir); ok {
		return pos
	}
	for _, name := range []string{".nugit/plan.yml", ".nugit/plan.yaml"} {
		if pos, ok := readPlan(filepath.Join(repoDir, name)); ok {
			return pos
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

func readPlan(path string) (model.PlanPosition, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return model.PlanPosition{}, false
	}
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
