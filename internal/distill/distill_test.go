package distill

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/knowledge"
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
