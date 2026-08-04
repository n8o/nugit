package distill

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// Two distinct lessons that slug identically must get distinct files + ids, and a
// multi-affects decision must scope global (P4 review findings).
func TestDistillSlugCollisionAndMultiAffectsScope(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")[:40]

	os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "c1\n\ndecision: cross-cutting choice\naffects: web, api\nlearned: cache invalidation!\nkeywords: x")
	os.WriteFile(filepath.Join(dir, "f"), []byte("c"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "c2\n\ndecision: another choice\nlearned: cache invalidation?\nkeywords: y")

	res, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Now: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	// both lessons promoted to DISTINCT files (slug collision disambiguated)
	if len(res.Lessons) != 2 {
		t.Fatalf("want 2 distinct lesson files, got %v", res.Lessons)
	}
	if res.Lessons[0] == res.Lessons[1] {
		t.Errorf("colliding lessons wrote the same path: %v", res.Lessons)
	}

	objs, _ := knowledge.Load(dir)
	ids := map[string]bool{}
	var crossCut bool
	for _, o := range objs {
		if ids[o.ID] {
			t.Errorf("duplicate id %s", o.ID)
		}
		ids[o.ID] = true
		if o.Type == "decision" && o.Scope == "global" {
			crossCut = true // the multi-affects (web, api) decision scoped global
		}
	}
	if !crossCut {
		t.Error("a decision with affects: web, api must scope global, not the first component")
	}
}

func TestDistillPromotesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feat(pkg): add P\n\ndecision: expose P\nrejected: keep it private\nlearned: API decisions belong in an ADR\naffects: pkg\nkeywords: api")

	opt := Options{RepoDir: dir, Base: base[:len(base)-1], Head: "HEAD", Now: "2026-01-01T00:00:00Z"}
	res, err := Distill(opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Decisions) != 1 || len(res.Lessons) != 1 {
		t.Fatalf("want 1 decision + 1 lesson, got %d/%d", len(res.Decisions), len(res.Lessons))
	}

	// the promoted ADR parses, has the right key + a constrains edge
	objs, err := knowledge.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var adr *struct{ found bool }
	for _, o := range objs {
		if o.ID == "ADR-0001" {
			adr = &struct{ found bool }{true}
			if o.Scope != "pkg" {
				t.Errorf("ADR scope = %q, want pkg", o.Scope)
			}
			has := false
			for _, e := range o.RelatesTo {
				if e == "constrains:pkg" {
					has = true
				}
			}
			if !has {
				t.Errorf("ADR missing constrains:pkg edge: %+v", o.RelatesTo)
			}
		}
	}
	if adr == nil {
		t.Fatal("ADR-0001 not found after distill")
	}

	// idempotent: a second run promotes nothing (already in the store)
	res2, err := Distill(opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Decisions) != 0 || len(res2.Lessons) != 0 {
		t.Errorf("re-distill must promote nothing; got %d/%d", len(res2.Decisions), len(res2.Lessons))
	}
}

// ADR-0018: the default MinRecur is 1 — a lone, decision-less `learned:` in a
// PR range promotes instead of dying in the write-only log.
func TestDistillMinRecurDefaultsToOne(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")[:40]

	os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "fix: thing\n\nlearned: never trust default timeouts\nkeywords: timeout, config")

	res, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Now: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lessons) != 1 || len(res.Decisions) != 0 {
		t.Fatalf("lone learned: must promote at default min-recur; got lessons=%v decisions=%v", res.Lessons, res.Decisions)
	}
}

// ADR-0018: an existing lesson sharing enough keywords dedupes the candidate at
// BOTH surfaces — Distill skips the write, Propose counts it as deduped.
func TestKeywordOverlapDedupe(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	existing := "---\nschema_version: 1\nid: LESSON-billing-timeouts\ntype: lesson\nscope: payments\nstatus: active\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n" +
		"# Lesson — billing timeouts\n\n**Trigger:** t\n\n**Insight:** never use library default timeouts for billing endpoints\n\n**Keywords:** payments, timeout, retry\n"
	os.MkdirAll(filepath.Join(dir, ".nugit", "lessons"), 0o755)
	os.WriteFile(filepath.Join(dir, ".nugit", "lessons", "billing-timeouts.md"), []byte(existing), 0o644)
	os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")[:40]

	// Different wording, overlapping keywords (timeout+payments = 2 shared,
	// covering 2/2 of the candidate's set) — same lesson, must not mint twice.
	os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "fix: charge\n\nlearned: set explicit timeouts on the charge path\nkeywords: timeout, payments")

	res, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Now: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lessons) != 0 || res.Skipped != 1 {
		t.Errorf("keyword-overlapping lesson must be skipped, got lessons=%v skipped=%d", res.Lessons, res.Skipped)
	}

	objs, _ := knowledge.Load(dir)
	commits := []model.Commit{{
		SHA: "abcdef1234567890", Subject: "fix: charge",
		Trailer: model.Trailer{Learned: "set explicit timeouts on the charge path", Keywords: []string{"timeout", "payments"}},
	}}
	props, deduped := Propose(commits, objs, 0)
	if len(props) != 0 || deduped != 1 {
		t.Errorf("Propose must dedupe by keyword overlap, got props=%v deduped=%d", props, deduped)
	}

	// A single shared keyword is NOT enough (rule needs >=2 shared).
	commits[0].Trailer.Keywords = []string{"timeout", "moxy"}
	props, deduped = Propose(commits, objs, 0)
	if len(props) != 1 || deduped != 0 {
		t.Errorf("one shared keyword must not dedupe, got props=%v deduped=%d", props, deduped)
	}
}

// ADR-0018: Propose is pure and mirrors Distill's selection — decisions first,
// then lessons, in-range deduped, with trailer metadata carried through.
func TestProposeSelection(t *testing.T) {
	commits := []model.Commit{
		{SHA: "1111111111", Subject: "feat(x): pick X", Trailer: model.Trailer{
			Decision: "pick X over Y", Rejected: "Y — slower", Learned: "X beats Y here",
			Affects: []string{"pkg"}, Keywords: []string{"x", "y"},
		}},
		{SHA: "2222222222", Subject: "fix: again", Trailer: model.Trailer{
			Learned: "X beats Y here", // in-range duplicate — collapses
		}},
		{SHA: "3333333333", Subject: "fix: lone insight", Trailer: model.Trailer{
			Learned: "cache keys must include the ref", Keywords: []string{"cache"},
		}},
	}
	props, deduped := Propose(commits, nil, 0)
	if deduped != 0 {
		t.Fatalf("empty store must dedupe nothing, got %d", deduped)
	}
	if len(props) != 3 {
		t.Fatalf("want decision + 2 lessons, got %+v", props)
	}
	if props[0].Kind != model.KindDecision || props[0].Text != "pick X over Y" || props[0].Scope != "pkg" || props[0].Commit != "11111111" {
		t.Errorf("decision proposal malformed: %+v", props[0])
	}
	if props[1].Kind != model.KindLesson || props[1].Text != "X beats Y here" {
		t.Errorf("significant lesson proposal malformed: %+v", props[1])
	}
	if props[2].Kind != model.KindLesson || props[2].Subject != "fix: lone insight" {
		t.Errorf("lone lesson must propose at min-recur 1: %+v", props[2])
	}
	// min-recur 2 restores the old filter: the lone lesson drops, the
	// decision-accompanied one stays.
	props, _ = Propose(commits, nil, 2)
	if len(props) != 2 {
		t.Errorf("min-recur 2 must drop the lone lesson, got %+v", props)
	}
}

// ADR-0016 candidate lane: distill mints proposed by default; -status ratified
// restores the pre-lane behavior; anything else is a hard error.
func TestDistillStatusLane(t *testing.T) {
	seed := func(t *testing.T) (dir, base string) {
		dir = t.TempDir()
		git(t, dir, "init", "-q")
		os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644)
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "base")
		base = git(t, dir, "rev-parse", "HEAD")[:40]
		os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644)
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", "c1\n\ndecision: pick X\nrejected: Y\nlearned: X beats Y here\naffects: pkg\nkeywords: x")
		return dir, base
	}

	read := func(t *testing.T, dir, rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	t.Run("default mints proposed", func(t *testing.T) {
		dir, base := seed(t)
		res, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Now: "2026-01-01T00:00:00Z"})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Decisions) != 1 || len(res.Lessons) != 1 {
			t.Fatalf("want 1 decision + 1 lesson, got %v / %v", res.Decisions, res.Lessons)
		}
		if d := read(t, dir, res.Decisions[0]); !strings.Contains(d, "status: proposed") {
			t.Errorf("decision must mint status: proposed, got:\n%s", d)
		}
		if l := read(t, dir, res.Lessons[0]); !strings.Contains(l, "status: proposed") {
			t.Errorf("lesson must mint status: proposed, got:\n%s", l)
		}
	})

	t.Run("ratified escape hatch", func(t *testing.T) {
		dir, base := seed(t)
		res, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Now: "2026-01-01T00:00:00Z", Status: "ratified"})
		if err != nil {
			t.Fatal(err)
		}
		if d := read(t, dir, res.Decisions[0]); !strings.Contains(d, "status: accepted") {
			t.Errorf("ratified decision must mint status: accepted, got:\n%s", d)
		}
		if l := read(t, dir, res.Lessons[0]); !strings.Contains(l, "status: active") {
			t.Errorf("ratified lesson must mint status: active, got:\n%s", l)
		}
	})

	t.Run("unknown status errors", func(t *testing.T) {
		dir, base := seed(t)
		if _, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Status: "draft"}); err == nil {
			t.Fatal("want error for unknown status")
		}
	})
}
