package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUntypedObjects(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/good.md",
		"---\nschema_version: 1\nid: ADR-1\ntype: decision\nscope: global\nstatus: accepted\n---\n\n# ok\n")
	// The JBS bug: supersedes as a YAML list fails the string-typed schema and
	// silently untypes the whole object.
	write(t, dir, ".nugit/decisions/list-supersedes.md",
		"---\nschema_version: 1\nid: ADR-2\ntype: decision\nsupersedes:\n  - ADR-1\n---\n\n# broken\n")
	write(t, dir, ".nugit/lessons/no-frontmatter.md", "# just prose, no fence\n")
	write(t, dir, ".nugit/decisions/.gitkeep", "")

	bad := untypedObjects(dir)
	if len(bad) != 2 {
		t.Fatalf("want 2 problem files, got %d: %v", len(bad), bad)
	}
	joined := strings.Join(bad, "\n")
	if !strings.Contains(joined, "list-supersedes.md") || !strings.Contains(joined, "no-frontmatter.md") {
		t.Fatalf("wrong files flagged: %v", bad)
	}
	if strings.Contains(joined, "good.md") {
		t.Fatalf("valid object flagged: %v", bad)
	}
}

func TestUntypedObjectsCheckInRun(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/list-supersedes.md",
		"---\nid: ADR-2\ntype: decision\nsupersedes:\n  - ADR-1\n---\n\n# broken\n")
	rep := Run(dir)
	found := false
	for _, c := range rep.Checks {
		if c.Name == "knowledge objects are typed" {
			found = true
			if c.OK {
				t.Error("check must fail when an object is silently untyped")
			}
			if !strings.Contains(c.Detail, "list-supersedes.md") {
				t.Errorf("detail should name the file: %s", c.Detail)
			}
		}
	}
	if !found {
		t.Fatal("knowledge-objects-typed check missing from doctor report")
	}
}
