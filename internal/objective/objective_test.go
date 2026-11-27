package objective

import (
	"reflect"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/model"
)

func commit(sha, objective string) model.Commit {
	return model.Commit{SHA: sha, Trailer: model.Trailer{Objective: objective}}
}

func store(lines ...string) beads.Store {
	return beads.Store{Issues: beads.ParseJSONL([]byte(strings.Join(lines, "\n")))}
}

func input(commits []model.Commit, files map[string][]string, changed ...model.FileChange) Input {
	return Input{
		Commits: commits,
		Files:   func(sha string) []string { return files[sha] },
		Code:    model.CodeDelta{Files: changed},
	}
}

// A range that names no objective has no view and no findings, so a repo whose
// agents do not name objectives renders exactly as before.
func TestNoObjectiveNoView(t *testing.T) {
	in := input([]model.Commit{commit("a", "")}, map[string][]string{"a": {"x.go"}},
		model.FileChange{Path: "x.go"})
	v := View(in)
	if v != nil {
		t.Fatalf("view = %+v, want nil", v)
	}
	if fs := Findings(v, true); fs != nil {
		t.Fatalf("findings = %+v, want none", fs)
	}
}

func TestViewResolvesTheBead(t *testing.T) {
	in := input(
		[]model.Commit{commit("a", "acme-7-2"), commit("b", "acme-7-2")},
		map[string][]string{"a": {"a/a.go"}, "b": {"a/a.go", "b/b.go", "gone.go"}},
		model.FileChange{Path: "a/a.go", Component: "compa"},
		model.FileChange{Path: "b/b.go", Component: "compb"},
	)
	in.Head = store(
		`{"id":"acme-7-1","title":"first","status":"closed","plan":"acme-7"}`,
		`{"id":"acme-7-2","title":"second","status":"closed","plan":"acme-7","acceptance_criteria":"it works"}`,
		`{"id":"acme-7-3","title":"third","status":"open","plan":"acme-7"}`,
	)
	in.Base = store(`{"id":"acme-7-2","title":"second","status":"in_progress","plan":"acme-7"}`)
	in.HasStore = true

	v := View(in)
	if v == nil || len(v.Objectives) != 1 {
		t.Fatalf("view = %+v, want one objective", v)
	}
	o := v.Objectives[0]
	if !o.Known || o.Title != "second" || o.Status != "closed" || o.BaseStatus != "in_progress" {
		t.Errorf("bead fields = %+v", o)
	}
	if o.Acceptance != "it works" || o.Plan != "acme-7" || o.PlanDone != 2 || o.PlanTotal != 3 {
		t.Errorf("plan fields = %+v", o)
	}
	// gone.go is not in the net diff, so it is not part of the objective's scope.
	if o.Commits != 2 || o.Files != 2 || strings.Join(o.Components, ",") != "compa,compb" {
		t.Errorf("scope = commits %d files %d comps %v", o.Commits, o.Files, o.Components)
	}
	if fs := Findings(v, true); len(fs) != 0 {
		t.Errorf("a focused PR has findings: %+v", fs)
	}
}

// A fixup commit with no trailer that touches a file the objective already
// touched stays in scope; a file only it touched is outside.
func TestOutsideIsOnlyUnclaimedFiles(t *testing.T) {
	in := input(
		[]model.Commit{commit("a", "acme-1"), commit("b", "")},
		map[string][]string{"a": {"a/a.go"}, "b": {"a/a.go", "c/c.go"}},
		model.FileChange{Path: "a/a.go", Component: "compa"},
		model.FileChange{Path: "c/c.go", Component: "compc"},
	)
	v := View(in)
	if strings.Join(v.Outside, ",") != "c/c.go" {
		t.Fatalf("outside = %v, want [c/c.go]", v.Outside)
	}
	fs := Findings(v, false)
	if len(fs) != 1 || fs[0].Check != Check || fs[0].Severity != model.SevWarn ||
		!strings.Contains(fs[0].Title, "1 file(s) changed outside") {
		t.Fatalf("findings = %+v", fs)
	}
}

func TestTwoObjectivesWarn(t *testing.T) {
	in := input(
		[]model.Commit{commit("a", "acme-1"), commit("b", "acme-2")},
		map[string][]string{"a": {"a.go"}, "b": {"b.go"}},
		model.FileChange{Path: "a.go"}, model.FileChange{Path: "b.go"},
	)
	fs := Findings(View(in), false)
	if len(fs) != 1 || !strings.Contains(fs[0].Title, "2 objectives (acme-1, acme-2)") {
		t.Fatalf("findings = %+v", fs)
	}
}

// An id the store does not hold warns only when there is a store to check.
func TestUnknownObjective(t *testing.T) {
	in := input([]model.Commit{commit("a", "acme-9")}, map[string][]string{"a": {"a.go"}},
		model.FileChange{Path: "a.go"})
	in.Head = store(`{"id":"acme-1","title":"other","status":"open"}`)
	v := View(in)
	if v.Objectives[0].Known {
		t.Fatal("acme-9 resolved against a store that lacks it")
	}
	if fs := Findings(v, false); len(fs) != 0 {
		t.Errorf("no store, findings = %+v", fs)
	}
	fs := Findings(v, true)
	if len(fs) != 1 || !strings.Contains(fs[0].Title, "acme-9 is not in the plan store") {
		t.Errorf("findings = %+v", fs)
	}
}

func obj(id string, kind model.Kind, scope string, extra func(*model.KnowledgeObject)) model.KnowledgeObject {
	o := model.KnowledgeObject{Body: "# " + id + " — " + id + " title\n\nbody"}
	o.ID, o.Type, o.Scope = id, kind, scope
	if extra != nil {
		extra(&o)
	}
	return o
}

func TestScopedKnowledge(t *testing.T) {
	in := input([]model.Commit{commit("a", "acme-1")}, map[string][]string{"a": {"a/a.go", "infra/x.yml"}},
		model.FileChange{Path: "a/a.go", Component: "compa"},
		model.FileChange{Path: "infra/x.yml"},
	)
	lesson := obj("LESSON-a", model.KindLesson, "compa", nil)
	changed := obj("ADR-0009", model.KindDecision, "compa", nil)
	in.AllObjects = []model.KnowledgeObject{
		obj("ADR-0002", model.KindDecision, "global", nil), // global: says nothing about this objective
		obj("ADR-0003", model.KindDecision, "compb", nil),  // another component
		obj("ADR-0004", model.KindDecision, "compb", func(o *model.KnowledgeObject) { o.RelatesTo = []string{"constrains:compa"} }),
		obj("ADR-0005", model.KindDecision, "compa", func(o *model.KnowledgeObject) { o.EffectiveStatus = model.StatusSuperseded }),
		obj("ADR-0006", model.KindDecision, "", func(o *model.KnowledgeObject) { o.AppliesToPaths = []string{"infra/**"} }),
		obj("SPEC-001", model.KindSpec, "compa", nil),
		lesson,
		changed,
	}
	in.Knowledge = model.KnowledgeDelta{Changes: []model.KnowledgeChange{{Status: "M", Object: &changed}}}

	refs := View(in).Objectives[0].Knowledge
	var got []string
	for _, r := range refs {
		got = append(got, r.ID)
	}
	// What the PR changed first, then decisions, then lessons, newest first.
	if want := "ADR-0009,ADR-0006,ADR-0004,LESSON-a"; strings.Join(got, ",") != want {
		t.Fatalf("knowledge = %v, want %s", got, want)
	}
	if !refs[0].New || refs[1].New || refs[0].Title != "ADR-0009 title" {
		t.Errorf("refs = %+v", refs)
	}
}

func TestKnowledgeIsCapped(t *testing.T) {
	in := input([]model.Commit{commit("a", "acme-1")}, map[string][]string{"a": {"a.go"}},
		model.FileChange{Path: "a.go", Component: "compa"})
	for i := 0; i < MaxKnowledge+3; i++ {
		in.AllObjects = append(in.AllObjects, obj(string(rune('A'+i)), model.KindDecision, "compa", nil))
	}
	o := View(in).Objectives[0]
	if len(o.Knowledge) != MaxKnowledge || o.KnowledgeMore != 3 {
		t.Fatalf("listed %d, more %d", len(o.Knowledge), o.KnowledgeMore)
	}
}

// A record governing more of the objective's scope outranks one that governs
// less, so the cap keeps the most specific.
func TestKnowledgeRanksByOverlap(t *testing.T) {
	in := input([]model.Commit{commit("a", "acme-1")}, map[string][]string{"a": {"a.go", "b.go"}},
		model.FileChange{Path: "a.go", Component: "compa"},
		model.FileChange{Path: "b.go", Component: "compb"})
	in.AllObjects = []model.KnowledgeObject{
		obj("ADR-0009", model.KindDecision, "compa", nil),
		obj("ADR-0001", model.KindDecision, "compa", func(o *model.KnowledgeObject) { o.RelatesTo = []string{"constrains:compb"} }),
	}
	refs := View(in).Objectives[0].Knowledge
	if len(refs) != 2 || refs[0].ID != "ADR-0001" {
		t.Fatalf("refs = %+v, want ADR-0001 first", refs)
	}
}

func TestTitleDropsTheID(t *testing.T) {
	for body, want := range map[string]string{
		"# ADR-0007 — Keep it pure\n": "Keep it pure",
		"# ADR-0007: Keep it pure\n":  "Keep it pure",
		"# Keep it pure\n":            "Keep it pure",
		"no heading\nmore":            "no heading",
		"# ADR-0007\n":                "ADR-0007",
	} {
		if got := title("ADR-0007", body); got != want {
			t.Errorf("title(%q) = %q, want %q", body, got, want)
		}
	}
}

func meets(sha, objective string, ids ...string) model.Commit {
	c := commit(sha, objective)
	c.Trailer.Meets = ids
	return c
}

// ADR-0066: each numbered criterion is met here, met earlier, or open.
func TestCriteriaStates(t *testing.T) {
	in := input(
		[]model.Commit{meets("aaaaaaaaa", "acme-1", "AC-1"), meets("bbbbbbbbb", "acme-1", "acme-1/AC-1", "AC-9", "other-2/AC-3")},
		map[string][]string{},
	)
	in.Head = store(`{"id":"acme-1","title":"t","status":"closed","acceptance_criteria":"AC-1: one\nAC-2: two\nAC-3: three"}`)
	in.Base = store(`{"id":"acme-1","title":"t","status":"in_progress"}`)
	in.HasStore = true
	asked := ""
	in.Earlier = func(id string) []model.Commit {
		asked = id
		return []model.Commit{meets("ccccccccc", "acme-1", "AC-2", "AC-7")}
	}
	v := View(in)
	o := v.Objectives[0]
	if asked != "acme-1" {
		t.Errorf("Earlier asked for %q", asked)
	}
	want := []model.Criterion{
		{ID: "AC-1", Text: "one", State: model.CriterionMetHere, By: []string{"aaaaaaa", "bbbbbbb"}},
		{ID: "AC-2", Text: "two", State: model.CriterionMetEarlier, By: []string{"ccccccc"}},
		{ID: "AC-3", Text: "three", State: model.CriterionOpen},
	}
	if !reflect.DeepEqual(o.Criteria, want) {
		t.Errorf("Criteria = %+v\nwant %+v", o.Criteria, want)
	}
	// An earlier commit's unknown id is not this PR's to fix.
	if strings.Join(o.UnknownMeets, ",") != "AC-9,other-2/AC-3" {
		t.Errorf("UnknownMeets = %q", o.UnknownMeets)
	}
	fs := Findings(v, true)
	var titles []string
	for _, f := range fs {
		titles = append(titles, f.Title)
	}
	got := strings.Join(titles, "\n")
	for _, w := range []string{"acme-1: 2 unknown criterion id(s) in meets:", "this PR closes acme-1 with 1 criterion(s) open: AC-3"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing finding %q in:\n%s", w, got)
		}
	}
}

// A bead that numbers no criteria keeps its one line and reads nothing
// earlier; a meets: against it is unknown.
func TestUnnumberedBeadUnchanged(t *testing.T) {
	in := input([]model.Commit{commit("a", "acme-1")}, map[string][]string{})
	in.Head = store(`{"id":"acme-1","title":"t","status":"closed","acceptance_criteria":"it works"}`)
	in.Base = store(`{"id":"acme-1","title":"t","status":"open"}`)
	in.HasStore = true
	in.Earlier = func(string) []model.Commit { t.Error("read earlier commits for an unnumbered bead"); return nil }
	v := View(in)
	if o := v.Objectives[0]; o.Criteria != nil || o.Acceptance != "it works" {
		t.Errorf("objective = %+v", o)
	}
	if fs := Findings(v, true); len(fs) != 0 {
		t.Errorf("findings = %+v", fs)
	}

	in.Commits = []model.Commit{meets("a", "acme-1", "AC-1")}
	fs := Findings(View(in), true)
	if len(fs) != 1 || !strings.Contains(fs[0].Detail, "numbers none") {
		t.Errorf("findings = %+v", fs)
	}
}

func TestMeetsWithoutObjective(t *testing.T) {
	v := View(input([]model.Commit{meets("abcdef12345", "", "AC-1")}, map[string][]string{}))
	if v == nil || strings.Join(v.MeetsWithoutObjective, ",") != "abcdef1" {
		t.Fatalf("view = %+v", v)
	}
	fs := Findings(v, true)
	if len(fs) != 1 || !strings.Contains(fs[0].Title, "name no objective") {
		t.Errorf("findings = %+v", fs)
	}
}

// Closing a bead whose criteria are all met warns about nothing; a bead
// already closed at base is not this PR's closing.
func TestClosingWithAllMet(t *testing.T) {
	in := input([]model.Commit{meets("a", "acme-1", "AC-1")}, map[string][]string{})
	in.Head = store(`{"id":"acme-1","title":"t","status":"closed","acceptance_criteria":"AC-1: one\nAC-2: two"}`)
	in.Base = store(`{"id":"acme-1","title":"t","status":"closed"}`)
	in.HasStore = true
	if fs := Findings(View(in), true); len(fs) != 0 {
		t.Errorf("already closed at base: %+v", fs)
	}
	in.Base = store(`{"id":"acme-1","title":"t","status":"open"}`)
	in.Commits = append(in.Commits, meets("b", "acme-1", "AC-2"))
	if fs := Findings(View(in), true); len(fs) != 0 {
		t.Errorf("all met: %+v", fs)
	}
}
