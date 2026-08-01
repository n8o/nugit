package retrieval

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir with identity/signing pinned so the test is hermetic.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func commitIn(t *testing.T, dir, rel, content string, msg ...string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", rel)
	c := []string{"commit"}
	for _, m := range msg {
		c = append(c, "-m", m)
	}
	gitIn(t, dir, c...)
}

// ADR-0024: when budget remains after the typed sections, the bundle appends
// recent commits touching the queried path — subject + decision:/learned:
// trailers — derived from git at read time, zero store writes. This is how an
// orphaned trailer (captured why with no store object) becomes retrievable.
func TestPathHistoryFillsRemainingBudget(t *testing.T) {
	dir := setup(t)
	gitIn(t, dir, "init")
	commitIn(t, dir, "internal/render/render.go", "v1", "add renderer")
	commitIn(t, dir, "internal/render/render.go", "v2",
		"fix(render): bump TAG rc4 -> rc5 — fixes the preview freeze",
		"decision: pin rc5; rc4 has the freeze regression\nlearned: preview freezes trace to the tag bump, not app code\nkeywords: render, freeze")
	commitIn(t, dir, "internal/util/util.go", "u", "touch util only")

	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.PathHistory) != 2 {
		t.Fatalf("want 2 history entries for the path, got %d: %+v", len(b.PathHistory), b.PathHistory)
	}
	h := b.PathHistory[0] // newest first
	if !strings.Contains(h.Subject, "fixes the preview freeze") {
		t.Errorf("newest-first subject, got %q", h.Subject)
	}
	if h.Decision != "pin rc5; rc4 has the freeze regression" {
		t.Errorf("decision trailer must surface, got %q", h.Decision)
	}
	if h.Learned != "preview freezes trace to the tag bump, not app code" {
		t.Errorf("learned trailer must surface, got %q", h.Learned)
	}
	if len(h.SHA) != 12 {
		t.Errorf("sha should be short (12), got %q", h.SHA)
	}
	for _, e := range b.PathHistory {
		if e.Subject == "touch util only" {
			t.Error("history must be scoped to the queried path")
		}
	}
	if b.Truncated || len(b.Dropped) != 0 {
		t.Errorf("nothing should drop at the default budget: %+v", b.Dropped)
	}

	md := b.Markdown()
	if !strings.Contains(md, "Recent capture on these paths") ||
		!strings.Contains(md, "learned: preview freezes trace to the tag bump") {
		t.Errorf("markdown must render the history section with trailers:\n%s", md)
	}
	js, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"path_history"`) {
		t.Errorf("bundle JSON must carry path_history: %s", js)
	}
}

// Not a git repo (or git failing) must degrade to an empty section — never an
// error, never a changed bundle shape.
func TestPathHistoryBestEffortWithoutGit(t *testing.T) {
	dir := setup(t) // plain TempDir, no .git
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.PathHistory) != 0 {
		t.Errorf("no repo -> no history, got %+v", b.PathHistory)
	}
}

// Path history is the LAST budget priority: when the budget is tight it drops
// before working memory and every typed section, and each drop is recorded.
func TestPathHistoryDroppedFirstWhenTight(t *testing.T) {
	b := &Bundle{
		C4:            C4Slice{Component: "c"},
		Decisions:     []Item{{ID: "D1", tokens: 30}},
		WorkingMemory: []string{"note"},
		PathHistory:   []HistoryEntry{{SHA: "abcdef123456", Subject: "s", tokens: 30}},
	}
	// baseline (~21) + decision (30) + wm note (~2) fit in 60; history does not.
	truncate(b, 60)
	if len(b.Decisions) != 1 || len(b.WorkingMemory) != 1 {
		t.Fatalf("decisions/working memory must survive: %d/%d", len(b.Decisions), len(b.WorkingMemory))
	}
	if len(b.PathHistory) != 0 || !b.Truncated {
		t.Fatalf("path history must drop first: history=%d truncated=%v", len(b.PathHistory), b.Truncated)
	}
	found := false
	for _, d := range b.Dropped {
		if strings.HasPrefix(d, "path-history abcdef123456") {
			found = true
		}
	}
	if !found {
		t.Errorf("the drop must be recorded like every other kind, got %v", b.Dropped)
	}

	// With room, the same bundle keeps the section and stays un-truncated.
	b2 := &Bundle{
		C4:          C4Slice{Component: "c"},
		Decisions:   []Item{{ID: "D1", tokens: 30}},
		PathHistory: []HistoryEntry{{SHA: "abcdef123456", Subject: "s", tokens: 30}},
	}
	truncate(b2, 200)
	if len(b2.PathHistory) != 1 || b2.Truncated {
		t.Fatalf("with budget remaining the section must fill: history=%d truncated=%v", len(b2.PathHistory), b2.Truncated)
	}
}

// ADR-0024: matches() moves from substring to whole-token semantics (the same
// tokenization working-memory hasKeyword always used). Before: task token
// "git" matched a lesson containing "digital". After: it must not — but an
// exact token hit still matches.
func TestTaskMatchingIsWholeToken(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/lessons/noise.md",
		obj("LESSON-NOISE", "lesson", "render", "digital signage rollout notes", ""))
	wf(t, dir, ".nugit/lessons/hit.md",
		obj("LESSON-HIT", "lesson", "render", "git hooks install ordering", ""))

	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go", Task: "git hook wiring"})
	if err != nil {
		t.Fatal(err)
	}
	l := ids(b.Lessons)
	if l["LESSON-NOISE"] {
		t.Error(`substring artifact: task token "git" must not match "digital"`)
	}
	if !l["LESSON-HIT"] {
		t.Errorf(`whole-token hit must still match: %v`, l)
	}
}

// Glossary line matching follows the same whole-token semantics.
func TestGlossaryMatchingIsWholeToken(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/glossary/g.md", obj("GLOSSARY-G", "glossary", "global",
		"- **digital** — signage twin\n- **gitops** — deploy from git\n", ""))

	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go", Task: "git deploy flow"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(b.Glossary, "\n")
	if strings.Contains(joined, "digital") {
		t.Errorf(`glossary substring artifact: "git" matched "digital": %q`, joined)
	}
	if !strings.Contains(joined, "gitops") {
		t.Errorf(`glossary whole-token hit ("git" appears in the definition) must match: %q`, joined)
	}
}
