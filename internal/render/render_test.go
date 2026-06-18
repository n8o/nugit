package render

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestConclusion(t *testing.T) {
	fail := model.Report{Findings: []model.Finding{{Severity: model.SevFail}}}
	warn := model.Report{Findings: []model.Finding{{Severity: model.SevWarn}}}
	clean := model.Report{}
	if Conclusion(fail) != "failure" {
		t.Error("fail finding → failure")
	}
	if Conclusion(warn) != "neutral" {
		t.Error("warn finding → neutral")
	}
	if Conclusion(clean) != "success" {
		t.Error("no findings → success")
	}
}

func TestMarkdownArchitectural(t *testing.T) {
	rep := model.Report{
		C4:   model.C4Delta{AddedComponents: []model.Component{{ID: "x", Name: "X"}}},
		Code: model.CodeDelta{Files: []model.FileChange{{Path: "a/a.go", Status: "M", Component: "a"}}, ByComp: map[string][]model.FileChange{"a": {{Path: "a/a.go", Status: "M"}}}},
		Findings: []model.Finding{{
			Check: "c4<->code", Severity: model.SevFail, Title: "undeclared dependency a → b", Detail: "x",
		}},
		Significance: model.Significance{Tier: model.TierArchitectural, Reasons: []string{"C4 model changed"}},
	}
	md := Markdown(rep)
	for _, want := range []string{"nugit — PR view", "architectural", "❌", "undeclared dependency a → b", "Architecture delta"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}

func TestMarkdownClean(t *testing.T) {
	rep := model.Report{
		Code:         model.CodeDelta{ByComp: map[string][]model.FileChange{}},
		Significance: model.Significance{Tier: model.TierTrivial, Reasons: []string{"small change"}},
	}
	md := Markdown(rep)
	if !strings.Contains(md, "✅ no inconsistencies") {
		t.Errorf("clean report should report no inconsistencies:\n%s", md)
	}
}
