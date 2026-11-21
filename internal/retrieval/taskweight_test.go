package retrieval

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// commonLessons adds n global lessons that share the task's common words.
// Their ids sort before LESSON-zz-*, so id order alone would serve them first.
func commonLessons(t *testing.T, dir string, n int) {
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("LESSON-a%02d", i)
		wf(t, dir, ".nugit/lessons/"+id+".md",
			obj(id, "lesson", "global", "a code graph lesson for agents", ""))
	}
}

func order(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

// The pilot's shape (ADR-0063): a watch lesson is found by its one rare word,
// and the task's common words match every other lesson too. With every match
// counted alike, id order served the first eight and the watch lesson never
// appeared. The rare word now puts it first.
func TestRareTaskWordRanksFirstWithinTier(t *testing.T) {
	dir := setup(t)
	commonLessons(t, dir, 12)
	wf(t, dir, ".nugit/lessons/zz.md",
		obj("LESSON-zz-graphify", "lesson", "global", "Graphify was evaluated as a code graph for agents", ""))

	var first []string
	for run := 0; run < 3; run++ {
		b, err := Context(Options{RepoDir: dir, Path: "internal/util/u.go", Task: "evaluate Graphify as a code graph for agents"})
		if err != nil {
			t.Fatal(err)
		}
		if len(b.Lessons) == 0 || b.Lessons[0].ID != "LESSON-zz-graphify" {
			t.Fatalf("the lesson matching the rare task word must lead: %v", order(b.Lessons))
		}
		if run == 0 {
			first = order(b.Lessons)
		} else if got := order(b.Lessons); !reflect.DeepEqual(got, first) {
			t.Fatalf("the order must be the same every run:\n%v\n%v", first, got)
		}
	}
}

// A word every record contains weighs nothing, so a task made only of such
// words orders exactly as before: by id. With no task at all, nothing has a
// weight.
func TestCommonWordsWeighNothing(t *testing.T) {
	dir := setup(t)
	commonLessons(t, dir, 3)
	tw := weigh(loadAll(t, dir), keywords("title"))
	for o, w := range tw.w {
		if w != 0 {
			t.Errorf("%s: a word in every record must weigh 0, got %v", o.ID, w)
		}
	}
	if tw := weigh(loadAll(t, dir), keywords("")); tw.given || tw.w != nil {
		t.Errorf("no task: want no weights, got %+v", tw)
	}

	b, err := Context(Options{RepoDir: dir, Path: "internal/util/u.go", Task: "title"})
	if err != nil {
		t.Fatal(err)
	}
	got := order(b.Lessons)
	want := []string{"LESSON-a00", "LESSON-a01", "LESSON-a02"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("all weights equal: want id order %v, got %v", want, got)
	}
}

// The weight orders within a rank and never across one (ADR-0063): a
// component-scoped lesson matching only a common word still outranks a global
// lesson matching the rarest word. Tiers are declared structure (ADR-0043); a
// word count does not overrule them.
func TestWeightNeverCrossesATier(t *testing.T) {
	dir := setup(t)
	commonLessons(t, dir, 3)
	wf(t, dir, ".nugit/lessons/comp.md",
		obj("LESSON-zz-render", "lesson", "render", "a code note for this component", ""))
	wf(t, dir, ".nugit/lessons/rare.md",
		obj("LESSON-zz-graphify", "lesson", "global", "Graphify was evaluated", ""))

	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go", Task: "evaluate Graphify code"})
	if err != nil {
		t.Fatal(err)
	}
	got := order(b.Lessons)
	if len(got) < 2 || got[0] != "LESSON-zz-render" || got[1] != "LESSON-zz-graphify" {
		t.Errorf("want the component lesson first, then the rare-word global: %v", got)
	}
}

// Matching is unchanged: an object has a weight exactly when it contains a
// whole task token (ADR-0024), so the weight cannot admit what the keyword
// gate keeps out.
func TestWeightMatchesExactlyWhatTheGateMatches(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/lessons/noise.md",
		obj("LESSON-NOISE", "lesson", "global", "digital signage rollout notes", ""))
	wf(t, dir, ".nugit/lessons/hit.md",
		obj("LESSON-HIT", "lesson", "global", "git hooks install ordering", ""))
	objs := loadAll(t, dir)
	tw := weigh(objs, keywords("git hook wiring"))
	for i := range objs {
		o := &objs[i]
		if want := overlaps(o.Body+" "+o.ID, keywords("git hook wiring")); tw.matches(o) != want {
			t.Errorf("%s: matches=%v, whole-token overlap=%v", o.ID, tw.matches(o), want)
		}
	}
	if !tw.matches(find(objs, "LESSON-HIT")) || tw.matches(find(objs, "LESSON-NOISE")) {
		t.Error(`"git" must match "git hooks" and never "digital"`)
	}
}

func loadAll(t *testing.T, dir string) []model.KnowledgeObject {
	t.Helper()
	objs, err := knowledge.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return objs
}

func find(objs []model.KnowledgeObject, id string) *model.KnowledgeObject {
	for i := range objs {
		if objs[i].ID == id {
			return &objs[i]
		}
	}
	return nil
}
