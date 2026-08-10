package doctor

// Tests for the ADR-0039 duplicate-id pre-flight check: two knowledge objects
// carrying one id are silent data loss, so the check GATES (like "knowledge
// objects are typed") and names every colliding file.

import (
	"strings"
	"testing"
)

func TestDuplicateIDsGateAndNameEveryFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/0007-accepted.md",
		"---\nschema_version: 1\nid: ADR-P-0007\ntype: decision\nscope: global\nstatus: accepted\n---\n\n# first\n")
	write(t, dir, ".nugit/decisions/0007-proposed.md",
		"---\nschema_version: 1\nid: ADR-P-0007\ntype: decision\nscope: global\nstatus: proposed\n---\n\n# second\n")
	write(t, dir, ".nugit/decisions/0008-unique.md",
		"---\nschema_version: 1\nid: ADR-P-0008\ntype: decision\nscope: global\nstatus: accepted\n---\n\n# fine\n")

	rep := Run(dir)
	c := checkByName(t, rep, "knowledge object ids are unique")
	if c.OK {
		t.Fatal("two objects sharing an id must fail the check")
	}
	if c.Advisory {
		t.Error("duplicate ids are silent data loss like an untyped object — the check must GATE, not advise (ADR-0039)")
	}
	if rep.AllOK() {
		t.Error("a gating failure must make AllOK false (doctor exits non-zero)")
	}
	if !strings.Contains(c.Detail, "ADR-P-0007") {
		t.Errorf("detail must name the colliding id, got %q", c.Detail)
	}
	// BOTH files, so the fix needs no search.
	for _, p := range []string{".nugit/decisions/0007-accepted.md", ".nugit/decisions/0007-proposed.md"} {
		if !strings.Contains(c.Detail, p) {
			t.Errorf("detail must name %s, got %q", p, c.Detail)
		}
	}
	if strings.Contains(c.Detail, "ADR-P-0008") {
		t.Errorf("a unique id must not appear in the finding, got %q", c.Detail)
	}
}

func TestDuplicateIDsSilentOnAHealthyStore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/a.md",
		"---\nschema_version: 1\nid: ADR-P-0001\ntype: decision\nscope: global\nstatus: accepted\n---\n\n# a\n")
	write(t, dir, ".nugit/decisions/b.md",
		"---\nschema_version: 1\nid: ADR-P-0002\ntype: decision\nscope: global\nstatus: accepted\n---\n\n# b\n")
	write(t, dir, ".nugit/lessons/l.md",
		"---\nschema_version: 1\nid: LESSON-p-one\ntype: lesson\nscope: global\nstatus: active\n---\n\n# l\n")

	c := checkByName(t, Run(dir), "knowledge object ids are unique")
	if !c.OK {
		t.Errorf("unique ids must pass, got %q", c.Detail)
	}
}

// Malformed front-matter leaves an id-less object; the grouping must not crash
// and must not report those files as sharing the empty id — they are the
// "knowledge objects are typed" check's business.
func TestDuplicateIDsIgnoresUntypedFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/bad-a.md", "---\nsupersedes:\n  - ADR-X\n---\n\n# bad a\n")
	write(t, dir, ".nugit/decisions/bad-b.md", "---\nsupersedes:\n  - ADR-Y\n---\n\n# bad b\n")
	write(t, dir, ".nugit/glossary.md", "no front matter at all\n")

	c := checkByName(t, Run(dir), "knowledge object ids are unique")
	if !c.OK {
		t.Errorf("id-less objects must not read as duplicates, got %q", c.Detail)
	}
}
