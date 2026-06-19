package distill

import (
	"os"
	"os/exec"
	"path/filepath"
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
