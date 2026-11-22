package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// Without -overlap the report carries no view, and the PR view says nothing
// about open work: a view rendered before ADR-0065 is unchanged.
func TestNoOverlapViewRendersNoSection(t *testing.T) {
	if out := Markdown(model.Report{}); strings.Contains(out, "🔀") || strings.Contains(out, "open work") {
		t.Fatalf("a report without an overlap view mentions it:\n%s", out)
	}
}

func TestOverlapSectionLeadsWithWhatBreaks(t *testing.T) {
	many := make([]string, 12)
	for i := range many {
		many[i] = fmt.Sprintf("f%02d.txt", i)
	}
	v := model.OverlapView{
		Source:  "open pull requests (gh)",
		Checked: 4,
		Unread:  []string{"#9"},
		Items: []model.OverlapItem{
			{With: model.OpenWork{PR: 94, Branch: "feat/x", Title: "Governed <config>", Draft: true},
				Overlap: model.Overlap{Collisions: []string{"both add ADR-0055: `a` here, `b` there"},
					Files: []string{"a.go", "b.go"}, MergeChecked: true, Conflicts: []string{"a.go"}}},
			{With: model.OpenWork{Branch: "origin/wip", Pushed: "2026-11-20"},
				Overlap: model.Overlap{Files: many, MergeChecked: true, Components: []string{"CLI", "Renderer"}}},
		},
	}
	out := Markdown(model.Report{Overlap: &v})
	for _, want := range []string{
		"<details open><summary>🔀 Open work that overlaps: 2 of 4 checked, 1 would collide or conflict</summary>",
		"Not read here, their heads are not in this checkout: #9.",
		"- ⚠️ **#94** `feat/x` Governed &lt;config&gt; (draft)",
		"  - both add ADR-0055: `a` here, `b` there",
		"  - conflicts in 1 of 2 shared files: `a.go`; also both change `b.go`",
		"- **origin/wip** (no open PR, pushed 2026-11-20)",
		"  - 12 shared files, merges cleanly: `f00.txt`",
		"`f09.txt`, and 2 more",
		"  - components: CLI, Renderer",
		"never judged",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestOverlapSummaryWhenNothingOverlaps(t *testing.T) {
	if s := OverlapSummary(model.OverlapView{Checked: 3}); s != "No open work overlaps this change (3 checked)" {
		t.Fatalf("summary = %q", s)
	}
	if s := OverlapSummary(model.OverlapView{Checked: 3, Pairwise: true}); s != "No two pieces of open work overlap (3 checked)" {
		t.Fatalf("pairwise summary = %q", s)
	}
	// Nothing to show is collapsed.
	if out := Markdown(model.Report{Overlap: &model.OverlapView{Checked: 3}}); !strings.Contains(out, "<details><summary>🔀 No open work") {
		t.Fatalf("an empty view is not collapsed:\n%s", out)
	}
}
