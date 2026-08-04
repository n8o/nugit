package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The duplication guard. The embedded tree is canonical and this repo's own
// .claude/skills/** is the INSTALLED artifact — nugit dogfooding its own
// installer. Two copies of one text is exactly the two-writers shape ADR-0011
// forbids, so the only thing that makes it acceptable is that drift fails here,
// by name, the moment either side is edited alone.
func TestRepoSkillsMatchTheEmbeddedCopies(t *testing.T) {
	list := All()
	if len(list) == 0 {
		t.Fatal("no skills embedded")
	}
	for _, s := range list {
		b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(s.Path)))
		if err != nil {
			t.Errorf("%s is not installed in this repo (run `nugit skill -install`): %v", s.Path, err)
			continue
		}
		if string(b) != s.Content {
			t.Errorf("SKILL DRIFT: %s differs from the embedded copy in internal/skills/data/%s/. "+
				"The embedded tree is canonical — edit it, then `nugit skill -install -force`.", s.Path, s.Name)
		}
	}
}

func TestEmbeddedSkillsAreWellFormed(t *testing.T) {
	names := map[string]bool{}
	for _, s := range All() {
		if names[s.Name] {
			t.Errorf("duplicate skill name %q", s.Name)
		}
		names[s.Name] = true
		if !strings.HasPrefix(s.Content, "---\nname: "+s.Name+"\n") {
			t.Errorf("%s: front matter must open with `name: %s` (the client keys on it):\n%.80s", s.Path, s.Name, s.Content)
		}
		if !strings.Contains(s.Content, "description:") {
			t.Errorf("%s: no description — a skill with none is never triggered", s.Path)
		}
		if s.Path != ".claude/skills/"+s.Name+"/SKILL.md" {
			t.Errorf("%s: unexpected install path", s.Path)
		}
	}
	for _, want := range []string{"nugit", "nugit-model"} {
		if !names[want] {
			t.Errorf("skill %q is not distributed by the binary", want)
		}
	}
}

func TestInstallWritesThenReportsUnchanged(t *testing.T) {
	dir := t.TempDir()
	first, err := Install(dir, false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range first {
		if !o.Created {
			t.Errorf("%s not created on a clean repo: %+v", o.Path, o)
		}
		b, rerr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(o.Path)))
		if rerr != nil || string(b) != o.Content {
			t.Errorf("%s content mismatch: %v", o.Path, rerr)
		}
	}
	// Re-running is quiet and idempotent, not a wall of "skipped".
	second, err := Install(dir, false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range second {
		if !o.Unchanged || o.Created || o.Skipped {
			t.Errorf("re-install of an identical file reported %+v, want Unchanged", o)
		}
	}
}

// A SKILL.md may carry local edits. nugit never merges prose it did not author,
// so a differing file is left alone until -force — the agentcfg contract.
func TestInstallNeverOverwritesLocalEditsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	s := All()[0]
	p := filepath.Join(dir, filepath.FromSlash(s.Path))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("locally edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Install(dir, false, s.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].Skipped {
		t.Fatalf("want Skipped, got %+v", res)
	}
	if b, _ := os.ReadFile(p); string(b) != "locally edited\n" {
		t.Fatal("a local edit was overwritten without -force")
	}

	res, err = Install(dir, true, s.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].Created {
		t.Fatalf("-force did not overwrite: %+v", res)
	}
	if b, _ := os.ReadFile(p); string(b) != s.Content {
		t.Error("-force wrote the wrong content")
	}
}

func TestInstallRejectsAnUnknownSkillName(t *testing.T) {
	if _, err := Install(t.TempDir(), false, "nope"); err == nil {
		t.Fatal("installed an unknown skill")
	}
}
