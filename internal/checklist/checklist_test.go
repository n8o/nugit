package checklist

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func item(id, check string, t model.DiffTrigger) model.KnowledgeObject {
	o := model.KnowledgeObject{Body: "# Lesson — " + id + " title\n"}
	o.ID, o.Type, o.Status, o.Scope, o.Check = id, model.KindLesson, model.StatusActive, "global", check
	o.DiffTrigger = &t
	return o
}

const diff = `diff --git a/internal/model/msg.go b/internal/model/msg.go
index 1..2 100644
--- a/internal/model/msg.go
+++ b/internal/model/msg.go
@@ -1,3 +1,4 @@
 type Msg struct {
-	Old string
+	Body string ` + "`json:\"body\"`" + `
+	Attempt int ` + "`json:\"attempt\"`" + `
 }
diff --git a/internal/retry/retry.go b/internal/retry/retry.go
deleted file mode 100644
--- a/internal/retry/retry.go
+++ /dev/null
@@ -1,2 +0,0 @@
---- a line that starts with dashes
-func Retry() {}
`

func TestParseDiff(t *testing.T) {
	got := ParseDiff(diff)
	m := got["internal/model/msg.go"]
	if len(m.Added) != 2 || len(m.Removed) != 1 || m.Removed[0] != "\tOld string" {
		t.Errorf("msg.go: %+v", m)
	}
	r := got["internal/retry/retry.go"]
	if len(r.Removed) != 2 || r.Removed[0] != "--- a line that starts with dashes" {
		t.Errorf("a deleted file keys by its old path, and a removed '---' line is content: %+v", r)
	}
}

// The article's example: when message types change, ask whether a new field
// loads safely from records written before it existed.
func TestMatch(t *testing.T) {
	items := []model.KnowledgeObject{
		item("LESSON-new-field-loads-old-records", "does a record written before this field existed still load?",
			model.DiffTrigger{Paths: []string{"internal/model/**"}, Added: "json:\""}),
		item("LESSON-retry-removed", "is a retry still needed somewhere?",
			model.DiffTrigger{Paths: []string{"internal/**"}, Removed: `func Retry\(`}),
		item("LESSON-any-model-change", "", model.DiffTrigger{Paths: []string{"internal/model/**"}}),
		item("LESSON-docs", "x", model.DiffTrigger{Paths: []string{"docs/**"}}),
		item("LESSON-bad-regexp", "x", model.DiffTrigger{Paths: []string{"**"}, Added: "("}),
	}
	files := []string{"internal/retry/retry.go", "internal/model/msg.go"}
	got := Match(items, files, ParseDiff(diff))
	if len(got) != 3 {
		t.Fatalf("want three matches, got %+v", got)
	}
	byID := map[string]model.ChecklistMatch{}
	for _, m := range got {
		byID[m.ID] = m
	}
	nf := byID["LESSON-new-field-loads-old-records"]
	if nf.Side != "added" || nf.File != "internal/model/msg.go" || nf.Line == "" || nf.Check == "" {
		t.Errorf("new-field: %+v", nf)
	}
	if rr := byID["LESSON-retry-removed"]; rr.Side != "removed" || rr.Line != "func Retry() {}" {
		t.Errorf("retry: %+v", rr)
	}
	if am := byID["LESSON-any-model-change"]; am.Side != "path" || am.Title != "LESSON-any-model-change title" {
		t.Errorf("a path-only trigger matches any change under its paths: %+v", am)
	}
	if _, ok := byID["LESSON-bad-regexp"]; ok {
		t.Error("an invalid pattern never matches")
	}
}

// An item is live, and a foreign one only from the hub, ratified and global.
func TestItems(t *testing.T) {
	local := item("L-1", "q", model.DiffTrigger{Paths: []string{"**"}})
	dead := item("L-2", "q", model.DiffTrigger{Paths: []string{"**"}})
	dead.EffectiveStatus = model.StatusSuperseded
	plain := item("L-3", "q", model.DiffTrigger{Paths: []string{"**"}})
	plain.DiffTrigger = nil
	hub := item("H-1", "q", model.DiffTrigger{Paths: []string{"**/*.sql"}})
	hub.Origin = "org"
	peer := item("P-1", "q", model.DiffTrigger{Paths: []string{"**"}})
	peer.Origin = "sibling"
	hubDraft := item("H-2", "q", model.DiffTrigger{Paths: []string{"**"}})
	hubDraft.Origin, hubDraft.Status = "org", model.StatusProposed

	got := Items([]model.KnowledgeObject{local, dead, plain, hub, peer, hubDraft}, "org")
	ids := []string{}
	for _, o := range got {
		ids = append(ids, o.ID)
	}
	if len(ids) != 2 || ids[0] != "L-1" || ids[1] != "H-1" {
		t.Errorf("want L-1 and H-1, got %v", ids)
	}
	if got := Items([]model.KnowledgeObject{hub}, ""); len(got) != 0 {
		t.Errorf("with no hub configured no foreign item fires: %v", got)
	}
}
