package delta

import (
	"os"

	"github.com/n8o/nugit/internal/model"
	"gopkg.in/yaml.v3"
)

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
