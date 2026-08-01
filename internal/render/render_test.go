package render

import (
	"encoding/json"
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

// ADR-0026: a downgraded run leads with the same notice in every format —
// first markdown line, check-run title prefix, and first structured-JSON field.
func TestDowngradeNoticeAllFormats(t *testing.T) {
	const notice = "enforcement downgraded by flag: config says fail, running with none"
	rep := model.Report{
		Enforcement:  model.NewEnforcementDowngrade("fail", "none"),
		BaseRef:      "base",
		Code:         model.CodeDelta{ByComp: map[string][]model.FileChange{}},
		Significance: model.Significance{Tier: model.TierTrivial, Reasons: []string{"small change"}},
	}
	if rep.Enforcement.Notice != notice {
		t.Fatalf("canonical notice drifted: %q", rep.Enforcement.Notice)
	}

	md := Markdown(rep)
	first := strings.SplitN(md, "\n", 2)[0]
	if !strings.Contains(first, notice) {
		t.Errorf("markdown must lead with the notice; first line: %q", first)
	}

	crb, err := CheckRunJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	var cr CheckRun
	if err := json.Unmarshal(crb, &cr); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cr.Title, notice) {
		t.Errorf("check-run title must start with the notice, got %q", cr.Title)
	}
	if !strings.Contains(cr.Summary, notice) {
		t.Error("check-run summary (markdown) must carry the notice")
	}

	sj, err := StructuredJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	s := string(sj)
	if !strings.Contains(s, notice) {
		t.Errorf("structured JSON must carry the notice:\n%s", s)
	}
	// Enforcement is the first field so the notice is on the JSON's first lines.
	if ei, bi := strings.Index(s, `"Enforcement"`), strings.Index(s, `"BaseRef"`); ei < 0 || bi < 0 || ei > bi {
		t.Errorf("Enforcement must lead the structured JSON (Enforcement@%d, BaseRef@%d)", ei, bi)
	}
}

// ADR-0026: no downgrade → no notice anywhere, and no Enforcement JSON key
// (undowngraded output stays byte-identical; the flat golden also guards this).
func TestNoDowngradeNoNotice(t *testing.T) {
	rep := model.Report{
		Code:         model.CodeDelta{ByComp: map[string][]model.FileChange{}},
		Significance: model.Significance{Tier: model.TierTrivial, Reasons: []string{"small change"}},
	}
	if strings.Contains(Markdown(rep), "enforcement downgraded") {
		t.Error("markdown must not mention a downgrade when none happened")
	}
	sj, err := StructuredJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sj), "Enforcement") {
		t.Errorf("nil Enforcement must be omitted from structured JSON:\n%s", sj)
	}
	crb, err := CheckRunJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(crb), "enforcement downgraded") {
		t.Error("check-run must not mention a downgrade when none happened")
	}
}
