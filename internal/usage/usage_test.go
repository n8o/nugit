package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/retrieval"
)

func TestAppendReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	r := Record{Time: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), Source: "cli",
		Path: "internal/foo/bar.go", Component: "foo", BudgetTokens: 4000,
		EstimatedTokens: 1200, Decisions: 3, Lessons: 1}
	if err := Append(dir, r); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := Append(dir, r); err != nil {
		t.Fatalf("Append second: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 records, got %d", len(got))
	}
	if got[0].Component != "foo" || got[0].Decisions != 3 {
		t.Fatalf("roundtrip mismatch: %+v", got[0])
	}
}

func TestBranchReferencesRoundtrip(t *testing.T) {
	dir := t.TempDir()
	r := Record{Time: time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC), Source: "mcp",
		Branch: "feat/x", Path: "internal/foo/bar.go", References: 2}
	if err := Append(dir, r); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := Read(dir)
	if err != nil || len(got) != 1 {
		t.Fatalf("Read: want 1 record, got %d (err %v)", len(got), err)
	}
	if got[0].Branch != "feat/x" || got[0].References != 2 {
		t.Fatalf("roundtrip mismatch: %+v", got[0])
	}
}

// TestReadOldFormatLine pins the compatibility contract: a log written before
// the branch/references fields existed must still parse, with zero values.
func TestReadOldFormatLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".nugit", ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"time":"2026-06-01T12:00:00Z","source":"cli","path":"internal/foo/bar.go","component":"foo","budget_tokens":4000,"estimated_tokens":900,"truncated":false,"decisions":2,"lessons":1,"glossary":0,"working_memory":0,"spec":true,"dropped":0}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, LogPath), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil || len(got) != 1 {
		t.Fatalf("Read: want 1 record, got %d (err %v)", len(got), err)
	}
	if got[0].Branch != "" || got[0].References != 0 {
		t.Fatalf("old-format line should yield zero values, got branch=%q references=%d",
			got[0].Branch, got[0].References)
	}
	if got[0].Component != "foo" || got[0].Decisions != 2 || !got[0].Spec {
		t.Fatalf("old-format fields lost: %+v", got[0])
	}
}

func TestFromBundleSetsReferences(t *testing.T) {
	b := retrieval.Bundle{Path: "a.go", References: []retrieval.Item{{ID: "REF-1"}, {ID: "REF-2"}}}
	r := FromBundle("cli", "task", b)
	if r.References != 2 {
		t.Fatalf("want References=2, got %d", r.References)
	}
	if r.Branch != "" {
		t.Fatalf("FromBundle must not stamp a branch (that is Log's job), got %q", r.Branch)
	}
}

func TestReadMissingLogIsEmpty(t *testing.T) {
	got, err := Read(t.TempDir())
	if err != nil || got != nil {
		t.Fatalf("missing log: want (nil, nil), got (%v, %v)", got, err)
	}
}

func TestReadSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, Record{Time: time.Now().UTC(), Source: "mcp", Path: "a.go"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, LogPath), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{half-written"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 valid record, got %d", len(got))
	}
}

func TestLogHonorsConfigOff(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".nugit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".nugit", "config.yml"),
		[]byte("usage:\n  log: off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Log(dir, "cli", "some task", retrieval.Bundle{Path: "a.go"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, LogPath)); !os.IsNotExist(err) {
		t.Fatalf("log written despite usage.log=off")
	}
}

func TestLogDefaultOnWritesRecord(t *testing.T) {
	dir := t.TempDir()
	b := retrieval.Bundle{Path: "a.go", Component: "foo", BudgetTokens: 4000, EstimatedTokens: 10}
	if err := Log(dir, "mcp", "add caching", b); err != nil {
		t.Fatalf("Log: %v", err)
	}
	got, err := Read(dir)
	if err != nil || len(got) != 1 {
		t.Fatalf("want 1 record, got %d (err %v)", len(got), err)
	}
	if got[0].Source != "mcp" || got[0].Task != "add caching" || got[0].Component != "foo" {
		t.Fatalf("record mismatch: %+v", got[0])
	}
}

func TestAggregate(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	records := []Record{
		{Time: base, Source: "cli", Component: "foo", EstimatedTokens: 100, Decisions: 1, Branch: "feat/a"},
		{Time: base.Add(time.Hour), Source: "mcp", Component: "foo", EstimatedTokens: 300, Spec: true, Truncated: true, Branch: "feat/a"},
		{Time: base.Add(2 * time.Hour), Source: "mcp", Component: "", EstimatedTokens: 200}, // unresolved + empty; no branch
		{Time: base.Add(-48 * time.Hour), Source: "cli", Component: "bar", EstimatedTokens: 900, Lessons: 2, Branch: "feat/b"},
	}
	s := Aggregate(records, base.Add(-time.Hour))
	if s.Calls != 3 {
		t.Fatalf("since filter: want 3 calls, got %d", s.Calls)
	}
	if s.BySource["mcp"] != 2 || s.BySource["cli"] != 1 {
		t.Fatalf("by source: %+v", s.BySource)
	}
	if s.Unresolved != 1 || s.EmptyBundles != 1 || s.Truncated != 1 {
		t.Fatalf("unresolved=%d empty=%d truncated=%d", s.Unresolved, s.EmptyBundles, s.Truncated)
	}
	if s.AvgTokens != 200 {
		t.Fatalf("avg tokens: want 200, got %d", s.AvgTokens)
	}
	if s.ByComponent["foo"] != 2 {
		t.Fatalf("by component: %+v", s.ByComponent)
	}
	if s.ByBranch["feat/a"] != 2 || len(s.ByBranch) != 1 {
		t.Fatalf("by branch: want {feat/a: 2} (empty branches excluded, feat/b outside window), got %+v", s.ByBranch)
	}

	all := Aggregate(records, time.Time{})
	if all.Calls != 4 {
		t.Fatalf("no filter: want 4, got %d", all.Calls)
	}
	if all.ByBranch["feat/a"] != 2 || all.ByBranch["feat/b"] != 1 || len(all.ByBranch) != 2 {
		t.Fatalf("by branch (all): empty-branch record must be excluded, got %+v", all.ByBranch)
	}
}

func TestStatsMarkdownEmpty(t *testing.T) {
	out := Stats{}.Markdown()
	if out == "" {
		t.Fatal("empty stats should still render guidance")
	}
}
