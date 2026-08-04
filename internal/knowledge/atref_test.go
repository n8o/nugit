package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeT(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const supersededByNew = `---
schema_version: 1
id: ADR-NEW
type: decision
scope: render
status: accepted
supersedes: ADR-0009
created: 2026-06-19T00:00:00Z
provenance:
  commit: def456
---

# ADR-NEW
`

// LoadAtRef reads the committed tree, not the disk: objects deleted from the
// working tree are still returned, uncommitted ones are not, paths come back
// nugit-root-relative (prefix stripped), .cache is skipped, and the supersedes
// graph resolves — all byte-compatible with Load.
func TestLoadAtRef(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	// nugit root nested at apps/op/ to exercise the prefix.
	writeT(t, dir, "apps/op/.nugit/decisions/0009.md", decision)
	writeT(t, dir, "apps/op/.nugit/decisions/new.md", supersededByNew)
	writeT(t, dir, "apps/op/.nugit/.cache/derived.md", decision)
	writeT(t, dir, "apps/op/.nugit/glossary.md", "# no front matter\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")

	// Diverge the working tree both ways: delete a committed object, add an
	// uncommitted one.
	if err := os.Remove(filepath.Join(dir, "apps/op/.nugit/decisions/0009.md")); err != nil {
		t.Fatal(err)
	}
	writeT(t, dir, "apps/op/.nugit/decisions/uncommitted.md", decision)

	repo := gitutil.Repo{Dir: dir}
	objs, err := LoadAtRef(repo, "HEAD", "apps/op/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Fatalf("want the 2 committed typed objects, got %d: %+v", len(objs), objs)
	}
	byID := map[string]model.KnowledgeObject{}
	for _, o := range objs {
		byID[o.ID] = o
	}
	old, ok := byID["ADR-0009"]
	if !ok {
		t.Fatal("ADR-0009 deleted from the working tree must still load from HEAD")
	}
	if old.Path != ".nugit/decisions/0009.md" {
		t.Errorf("path must be nugit-root-relative like Load's, got %q", old.Path)
	}
	if old.EffectiveStatus != model.StatusSuperseded {
		t.Errorf("supersedes graph must resolve: got %s", old.EffectiveStatus)
	}
}

// A bad ref is an error, never silently an empty corpus.
func TestLoadAtRefBadRef(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	writeT(t, dir, "x.txt", "x\n")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	if _, err := LoadAtRef(gitutil.Repo{Dir: dir}, "no-such-ref", ""); err == nil {
		t.Fatal("expected an error for a nonexistent ref")
	}
}
