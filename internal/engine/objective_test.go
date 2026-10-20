package engine

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/render"
)

// ADR-0048 end to end: commits naming an objective get the Objective section,
// with scope attributed per commit; a file only an unnamed commit touched is
// flagged; a range that names nothing renders as before.
func TestObjectiveEndToEnd(t *testing.T) {
	dir, seedBase := seed(t, false)
	write(t, dir, ".beads/issues.jsonl",
		`{"id":"demo-1-1","title":"make A faster","status":"in_progress","plan":"demo-1","acceptance_criteria":"A runs in O(1)"}`+"\n"+
			`{"id":"demo-1-2","title":"later","status":"open","plan":"demo-1"}`+"\n")
	write(t, dir, ".nugit/decisions/0001-a.md", "---\nschema_version: 1\nid: ADR-0001\ntype: decision\nscope: compa\nstatus: accepted\n---\n\n# A stays pure\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "chore: plan")
	base := git(t, dir, "rev-parse", "HEAD")

	write(t, dir, "a/a.go", "package a\n\n// A does a thing faster.\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "perf(a): faster A", "-m", "objective: demo-1-1")
	write(t, dir, "b/b.go", "package b\n\n// B is unrelated.\nfunc B() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "chore(b): unrelated tweak")
	head := git(t, dir, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Objective == nil || len(rep.Objective.Objectives) != 1 {
		t.Fatalf("objective = %+v", rep.Objective)
	}
	o := rep.Objective.Objectives[0]
	if !o.Known || o.Title != "make A faster" || o.Acceptance != "A runs in O(1)" || o.PlanTotal != 2 {
		t.Errorf("bead = %+v", o)
	}
	if strings.Join(o.Components, ",") != "compa" || o.Files != 1 {
		t.Errorf("scope = %v files %d", o.Components, o.Files)
	}
	if len(o.Knowledge) != 1 || o.Knowledge[0].ID != "ADR-0001" || o.Knowledge[0].Title != "A stays pure" {
		t.Errorf("knowledge = %+v", o.Knowledge)
	}
	if strings.Join(rep.Objective.Outside, ",") != "b/b.go" {
		t.Errorf("outside = %v", rep.Objective.Outside)
	}
	var focus []model.Finding
	for _, f := range rep.Findings {
		if f.Check == "objective-focus" {
			focus = append(focus, f)
		}
	}
	if len(focus) != 1 || !strings.Contains(focus[0].Detail, "b/b.go") {
		t.Errorf("objective-focus findings = %+v", focus)
	}
	if md := render.Markdown(rep); !strings.Contains(md, "**demo-1-1** — make A faster") {
		t.Errorf("markdown missing the objective:\n%s", md)
	}

	// A range whose commits name no objective: no section, no finding.
	rep, err = BuildReport(Options{RepoDir: dir, Base: seedBase, Head: base})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Objective != nil {
		t.Errorf("unnamed range has an objective: %+v", rep.Objective)
	}
	for _, f := range rep.Findings {
		if f.Check == "objective-focus" {
			t.Errorf("unnamed range has a focus finding: %+v", f)
		}
	}
}
