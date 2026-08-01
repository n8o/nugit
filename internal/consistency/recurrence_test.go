package consistency

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
)

// gitT runs git in dir with identity pinned; dates optionally backdated so
// window-boundary behavior is testable against the real clock.
func gitT(t *testing.T, dir, date string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t",
		"-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	cmd.Env = os.Environ()
	if date != "" {
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func commitFile(t *testing.T, dir, rel, content, msg, date string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, date, "add", "-A")
	gitT(t, dir, date, "commit", "-q", "-m", msg)
}

// recurrenceRepo builds a repo whose component a accumulated fix churn.
// msgs are commit messages, applied in order as edits to a/a.go.
func recurrenceRepo(t *testing.T, msgs []string, dates []string) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "", "init", "-q")
	commitFile(t, dir, "a/a.go", "package a\n", "base", "")
	for i, m := range msgs {
		d := ""
		if dates != nil {
			d = dates[i]
		}
		commitFile(t, dir, "a/a.go", "package a\n// edit "+m+"\n", m, d)
	}
	return dir
}

func recurrenceInput(dir string, objs []model.KnowledgeObject) Input {
	return Input{
		Repo: gitutil.Repo{Dir: dir},
		Head: "HEAD",
		HeadModel: model.Model{Components: []model.Component{
			{ID: "a", Paths: []string{"a/**"}},
		}},
		Code: model.CodeDelta{Files: []model.FileChange{
			{Path: "a/a.go", Status: "M", Component: "a"},
		}},
		AllObjects: objs,
		Recurrence: RecurrenceOpts{Enabled: true, WindowDays: 90, MinFixes: 3},
	}
}

func TestRecurrenceFires(t *testing.T) {
	dir := recurrenceRepo(t, []string{
		"fix(a): first regression",
		"fix(a): second regression",
		"fix(a): third regression",
	}, nil)
	fs := checkRecurrence(recurrenceInput(dir, nil))
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(fs), fs)
	}
	f := fs[0]
	if f.Check != "recurrence" || f.Severity != model.SevWarn {
		t.Errorf("check/severity: %s/%s", f.Check, f.Severity)
	}
	if !strings.Contains(f.Title, "3 fix-typed commits on a") {
		t.Errorf("title: %q", f.Title)
	}
	if !strings.Contains(f.Detail, "third regression") || !strings.Contains(f.Detail, "nugit distill") {
		t.Errorf("detail should list fixes and suggest capture: %q", f.Detail)
	}
	if strings.Contains(f.Detail, "nugit reinforce") {
		t.Errorf("no governing knowledge — must not suggest reinforce: %q", f.Detail)
	}
}

func TestRecurrenceSuggestsReinforceWhenGoverned(t *testing.T) {
	dir := recurrenceRepo(t, []string{
		"fix(a): one", "fix(a): two", "revert(a): three",
	}, nil)
	old := model.KnowledgeObject{
		FrontMatter: model.FrontMatter{
			ID: "LESSON-old", Type: model.KindLesson, Scope: "a",
			Status:  model.StatusActive,
			Created: time.Now().AddDate(0, 0, -200), // predates the window
		},
		EffectiveStatus: model.StatusActive,
	}
	fs := checkRecurrence(recurrenceInput(dir, []model.KnowledgeObject{old}))
	if len(fs) != 1 {
		t.Fatalf("want 1 finding, got %d", len(fs))
	}
	if !strings.Contains(fs[0].Detail, "nugit reinforce LESSON-old") {
		t.Errorf("should point at the too-narrow governing lesson: %q", fs[0].Detail)
	}
}

func TestRecurrenceSilent(t *testing.T) {
	threeFixes := []string{"fix(a): one", "fix(a): two", "fix(a): three"}

	t.Run("fresh-capture-suppresses", func(t *testing.T) {
		dir := recurrenceRepo(t, threeFixes, nil)
		fresh := model.KnowledgeObject{
			FrontMatter: model.FrontMatter{
				ID: "LESSON-fresh", Type: model.KindLesson, Scope: "a",
				Status: model.StatusActive, Created: time.Now().AddDate(0, 0, -1),
			},
			EffectiveStatus: model.StatusActive,
		}
		if fs := checkRecurrence(recurrenceInput(dir, []model.KnowledgeObject{fresh})); len(fs) != 0 {
			t.Errorf("in-window capture must suppress: %+v", fs)
		}
	})

	t.Run("trailer-capture-suppresses", func(t *testing.T) {
		dir := recurrenceRepo(t, []string{
			"fix(a): one", "fix(a): two",
			"fix(a): three\n\nlearned: the real invariant\nkeywords: a, invariant",
		}, nil)
		if fs := checkRecurrence(recurrenceInput(dir, nil)); len(fs) != 0 {
			t.Errorf("learned: trailer in the window must suppress: %+v", fs)
		}
	})

	t.Run("below-threshold", func(t *testing.T) {
		dir := recurrenceRepo(t, []string{"fix(a): one", "fix(a): two"}, nil)
		if fs := checkRecurrence(recurrenceInput(dir, nil)); len(fs) != 0 {
			t.Errorf("2 fixes < min 3 must stay silent: %+v", fs)
		}
	})

	t.Run("outside-window", func(t *testing.T) {
		old := time.Now().AddDate(0, 0, -120).Format(time.RFC3339)
		dir := recurrenceRepo(t, threeFixes, []string{old, old, old})
		if fs := checkRecurrence(recurrenceInput(dir, nil)); len(fs) != 0 {
			t.Errorf("fixes older than the window must not count: %+v", fs)
		}
	})

	t.Run("non-fix-subjects", func(t *testing.T) {
		dir := recurrenceRepo(t, []string{"feat(a): one", "chore(a): two", "refactor(a): three"}, nil)
		if fs := checkRecurrence(recurrenceInput(dir, nil)); len(fs) != 0 {
			t.Errorf("non-fix churn must stay silent: %+v", fs)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		dir := recurrenceRepo(t, threeFixes, nil)
		in := recurrenceInput(dir, nil)
		in.Recurrence.Enabled = false
		if fs := checkRecurrence(in); fs != nil {
			t.Errorf("disabled must return nil: %+v", fs)
		}
	})
}

func TestFixTyped(t *testing.T) {
	yes := []string{
		"fix: thing", "fix(scope): thing", "FIX(scope): thing", "fix!: breaking",
		"revert: earlier", "revert(pool): re-stage", `Revert "feat: x"`,
	}
	no := []string{
		"feat: fix the docs", "prefix: something", "fixture: add", "hotfix stuff",
		"chore(fix): rename", "unrelated",
	}
	for _, s := range yes {
		if !fixTyped(s) {
			t.Errorf("want fix-typed: %q", s)
		}
	}
	for _, s := range no {
		if fixTyped(s) {
			t.Errorf("want NOT fix-typed: %q", s)
		}
	}
}
