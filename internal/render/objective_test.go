package render

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestObjectiveSection(t *testing.T) {
	v := model.ObjectiveView{
		Objectives: []model.Objective{{
			ID: "acme-7-2", Known: true, Title: "second <b>", Status: "closed", BaseStatus: "in_progress",
			Acceptance: "it works", Plan: "acme-7", PlanDone: 2, PlanTotal: 3,
			Commits: 2, Files: 3, Components: []string{"compa", "compb"},
			Knowledge:     []model.ObjectiveRef{{ID: "ADR-0009", Type: model.KindDecision, Title: "why", New: true}},
			KnowledgeMore: 4,
		}},
		Outside: []string{"c.go"},
	}
	out := objectiveSection(v)
	for _, want := range []string{
		"### 🎯 Objective",
		"**acme-7-2** — second &lt;b&gt; · in_progress → **closed**",
		"done when: it works",
		"plan **acme-7**: 2/3 steps done",
		"this PR: 2 commits, 3 files in compa, compb",
		"decision `ADR-0009` — why 🆕",
		"_+4 more_",
		"Outside the objective: 1 file",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestObjectiveSectionUnknownAndNew(t *testing.T) {
	out := objectiveSection(model.ObjectiveView{Objectives: []model.Objective{
		{ID: "acme-9", Commits: 1, Files: 1},
		{ID: "acme-3", Known: true, Title: "t", Status: "open", Commits: 1},
	}})
	if !strings.Contains(out, "**acme-9** · _not in the plan store_") {
		t.Errorf("unknown objective:\n%s", out)
	}
	if !strings.Contains(out, "**open** (new in this PR)") {
		t.Errorf("new bead:\n%s", out)
	}
}

// No objective → no section: the view is byte-identical to before ADR-0048.
func TestMarkdownWithoutObjective(t *testing.T) {
	if strings.Contains(Markdown(model.Report{}), "Objective") {
		t.Error("a report with no objective rendered the section")
	}
	out := Markdown(model.Report{Objective: &model.ObjectiveView{Objectives: []model.Objective{{ID: "acme-1"}}}})
	if !strings.Contains(out, "### 🎯 Objective") {
		t.Errorf("section missing:\n%s", out)
	}
}
