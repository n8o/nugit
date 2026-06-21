package notion

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateBodyShape(t *testing.T) {
	d := Doc{GitID: "ADR-0011", Title: "Single-writer-per-fact", Markdown: "# Single-writer-per-fact\n\nbody"}
	b, _ := json.Marshal(CreateBody("db123", d))
	s := string(b)

	for _, want := range []string{
		`"database_id":"db123"`,
		`"markdown":"# Single-writer-per-fact\n\nbody"`,
		`"nugit_git_id":[{"text":{"content":"ADR-0011"}`, // idempotency key as a rich_text prop
		`"title":[{"text":{"content":"Single-writer-per-fact"}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("create body missing %q:\n%s", want, s)
		}
	}
}

func TestReplaceBodyShape(t *testing.T) {
	b, _ := json.Marshal(ReplaceBody("# new\n\ncontent"))
	s := string(b)
	if !strings.Contains(s, `"type":"replace_content"`) || !strings.Contains(s, `"new_str":"# new\n\ncontent"`) {
		t.Errorf("replace body wrong: %s", s)
	}
}

func TestQueryBodyFiltersByGitID(t *testing.T) {
	b, _ := json.Marshal(queryBody("LESSON-x"))
	s := string(b)
	if !strings.Contains(s, `"property":"nugit_git_id"`) || !strings.Contains(s, `"equals":"LESSON-x"`) {
		t.Errorf("query filter wrong: %s", s)
	}
}

func TestCollectDocs(t *testing.T) {
	dir := t.TempDir()
	wf := func(rel, body string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	hdr := func(id, typ string) string {
		return "---\nschema_version: 1\nid: " + id + "\ntype: " + typ +
			"\nscope: global\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# " + id + " title\n\nbody\n"
	}
	wf(".nugit/decisions/a.md", hdr("ADR-1", "decision"))
	wf(".nugit/lessons/b.md", hdr("LESSON-1", "lesson"))
	wf(".nugit/glossary.md", hdr("GLOSSARY", "glossary")) // glossary excluded

	docs, err := CollectDocs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("want 2 docs (decision+lesson, glossary excluded), got %d: %+v", len(docs), docs)
	}
	// sorted by id, title from the first heading
	if docs[0].GitID != "ADR-1" || docs[0].Title != "ADR-1 title" {
		t.Errorf("doc[0] wrong: %+v", docs[0])
	}
}
