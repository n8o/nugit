package localmem

import "testing"

func TestAppendAndRecentNewestFirst(t *testing.T) {
	dir := t.TempDir()
	if got := Recent(dir, 10); got != nil {
		t.Errorf("empty store should yield nil, got %v", got)
	}
	for _, txt := range []string{"first", "second", "third"} {
		if err := Append(dir, Entry{Text: txt, Scope: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got := Recent(dir, 10)
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d", len(got))
	}
	if got[0].Text != "third" || got[2].Text != "first" {
		t.Errorf("not newest-first: %v", []string{got[0].Text, got[1].Text, got[2].Text})
	}
	if got[0].Kind != "note" || got[0].Time == "" {
		t.Errorf("defaults not applied: %+v", got[0])
	}
	if len(Recent(dir, 2)) != 2 {
		t.Error("max not honored")
	}
}
