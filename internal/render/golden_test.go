package render

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// StructuredJSON of a report with NO container delta must stay byte-identical
// across the containers-become-first-class change: the new C4Delta container
// fields are omitempty, so a flat model's JSON contract cannot drift.
// Regenerate deliberately with: go test ./internal/render -run Golden -update

var update = flag.Bool("update", false, "rewrite golden files")

// flatReport is a fixed, fully-deterministic report over a flat (container-free)
// model — every field a real pr-render would populate on a flat repo.
func flatReport() model.Report {
	return model.Report{
		BaseRef: "base", HeadRef: "head",
		Commits: []model.Commit{{
			SHA: "0123456789abcdef", Subject: "feat: x",
			Trailer: model.Trailer{Decision: "d", Learned: "l", Keywords: []string{"k"}},
		}},
		C4: model.C4Delta{
			AddedComponents: []model.Component{{ID: "b", Name: "B", Paths: []string{"b/**"}}},
			AddedRels:       []model.Relationship{{Src: "a", Dst: "b", Desc: "uses"}},
			ChangedComponents: []model.ComponentChange{{
				Before: model.Component{ID: "a", Name: "A"},
				After:  model.Component{ID: "a", Name: "A2"},
				Fields: []string{"name"},
			}},
		},
		Code: model.CodeDelta{
			Files: []model.FileChange{{Path: "a/a.go", Added: 3, Deleted: 1, Status: "M", Component: "a"}},
			ByComp: map[string][]model.FileChange{
				"a": {{Path: "a/a.go", Added: 3, Deleted: 1, Status: "M", Component: "a"}},
			},
			TotalAdd: 3, TotalDel: 1,
		},
		Findings: []model.Finding{{
			Check: "c4<->code", Severity: model.SevFail,
			Title: "undeclared dependency a → c", Detail: "detail",
		}},
		Significance: model.Significance{Tier: model.TierArchitectural, Reasons: []string{"model changed"}},
	}
}

func TestStructuredJSONFlatGolden(t *testing.T) {
	got, err := StructuredJSON(flatReport())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "Container") {
		t.Errorf("flat report leaked container artifacts into structured JSON:\n%s", got)
	}
	const path = "testdata/structured_flat.golden"
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("structured JSON drifted from golden — flat-model output must stay byte-identical\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
