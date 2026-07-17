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

func TestStructuredJSONExcludesHeadModel(t *testing.T) {
	tiered := model.KnowledgeObject{FrontMatter: model.FrontMatter{ID: "ADR-1", Type: model.KindDecision, Scope: "secret"}}
	tiered.Evidence = model.EvidenceEnforced
	rep := model.Report{
		HeadModel: model.Model{Components: []model.Component{
			{ID: "secret", Paths: []string{"internal/secret/**"}},
		}},
		Knowledge: model.KnowledgeDelta{Changes: []model.KnowledgeChange{
			{Path: ".nugit/decisions/1.md", Status: "A", Object: &tiered},
		}},
	}
	out, err := StructuredJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "HeadModel") || strings.Contains(string(out), "internal/secret") {
		t.Errorf("HeadModel/path globs leaked into structured JSON:\n%s", out)
	}
	// The evidence tier is a bare label — it must appear WITHOUT dragging any
	// model/glob data along.
	if !strings.Contains(string(out), "enforced") {
		t.Errorf("evidence tier missing from structured JSON:\n%s", out)
	}
}

// The knowledge bullet carries the tier when the object is annotated.
func TestMarkdownKnowledgeTier(t *testing.T) {
	o := model.KnowledgeObject{FrontMatter: model.FrontMatter{ID: "ADR-1", Type: model.KindDecision, Scope: "a", Status: model.StatusAccepted}}
	o.EffectiveStatus = model.StatusAccepted
	o.Evidence = model.EvidenceChecked
	rep := model.Report{
		Code:         model.CodeDelta{ByComp: map[string][]model.FileChange{}},
		Knowledge:    model.KnowledgeDelta{Changes: []model.KnowledgeChange{{Path: "x.md", Status: "A", Object: &o}}},
		Significance: model.Significance{Tier: model.TierFeature, Reasons: []string{"knowledge changed"}},
	}
	md := Markdown(rep)
	if !strings.Contains(md, "(a, accepted, checked)") {
		t.Errorf("knowledge bullet missing tier:\n%s", md)
	}
}
