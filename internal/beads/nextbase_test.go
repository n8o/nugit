package beads

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// committedAs is the store HEAD holds once an export's shards are committed.
func committedAs(out map[string][]byte) Store {
	var files []string
	for f := range out {
		files = append(files, f)
	}
	sort.Strings(files)
	return fromFiles(files, out)
}

// twoExports runs an export scoped to first, commits its shards, saves the
// base Merge's caller is told to save, and exports the same database again
// scoped to second: a session that files a follow-up in another plan, then
// names that plan with -plans.
func twoExports(t *testing.T, fresh []string, first, second Scope) (base1 []Issue, got Store, rep MergeReport) {
	t.Helper()
	db := storeOf(lines(fresh...))
	merged, _ := Merge(db, headStore(), lines(baseExport...), first)
	out, err := Normalize(merged, true)
	if err != nil {
		t.Fatal(err)
	}
	base1 = ParseJSONL(NextBase(db, headStore(), lines(baseExport...), first))
	got, rep = Merge(storeOf(lines(fresh...)), committedAs(out), base1, second)
	return base1, got, rep
}

// The gap ADR-0064 closes. A scoped export leaves another plan's database
// edits out, and they are still edits: the base saved after it must not
// record them as shared with HEAD. With bd's raw export as the base, the next
// export that names the plan read the bead filed there (acme-mesh-5) as
// deleted on HEAD and dropped it, and the update there (acme-dock-2) as a
// database behind HEAD and reverted it. Both silently: bd still had them.
func TestNextBaseKeepsWhatTheScopeLeftOutPending(t *testing.T) {
	both := Scope{"acme-rift": true, "acme-mesh": true, "acme-dock": true}
	base1, got, rep := twoExports(t, secondSession, Scope{"acme-rift": true}, both)

	m := byID(got)
	if _, ok := m["acme-mesh-5"]; !ok {
		t.Error("the bead filed in another plan was dropped once that plan was named")
	}
	if s := m["acme-dock-2"].Status; s != "in_progress" {
		t.Errorf("the edit in another plan was reverted once that plan was named: status %q", s)
	}
	want := MergeReport{Missing: []string{"acme-dock-3"}, Committed: []string{"acme-rift-1"}}
	if !reflect.DeepEqual(rep, want) {
		t.Errorf("report = %+v\nwant     %+v", rep, want)
	}

	b := byID(Store{Issues: base1})
	if s := b["acme-rift-2"].Status; s != "closed" {
		t.Errorf("in scope the base is the export's line: acme-rift-2 is %q", s)
	}
	if s := b["acme-dock-2"].Status; s != "open" {
		t.Errorf("outside the scope the base keeps its line while the edit is pending: acme-dock-2 is %q", s)
	}
	if _, ok := b["acme-mesh-5"]; ok {
		t.Error("a bead the export did not write is not in the base")
	}
	if got, want := string(b["acme-mesh-1"].src), baseExport[2]; got != want {
		t.Errorf("where the database and HEAD agree outside the scope, their line is the base:\n got  %s\n want %s", got, want)
	}
}

// A bd delete in another plan is pending the same way: the bead stays in the
// base, so the export that names its plan carries the delete through.
func TestNextBaseKeepsADeleteOutsideTheScopePending(t *testing.T) {
	fresh := append(append([]string(nil), baseExport[:2]...), baseExport[3:]...) // another session deleted acme-mesh-1
	base1, got, rep := twoExports(t, fresh, Scope{"acme-rift": true}, Scope{"acme-mesh": true})
	if _, ok := byID(Store{Issues: base1})["acme-mesh-1"]; !ok {
		t.Error("the base dropped a bead whose delete the export did not carry")
	}
	if _, ok := byID(got)["acme-mesh-1"]; ok {
		t.Error("the delete was lost once its plan was named")
	}
	if !reflect.DeepEqual(rep.Deleted, []string{"acme-mesh-1"}) {
		t.Errorf("deleted = %v", rep.Deleted)
	}
}

// Unscoped, the next base is the raw export, line for line: ADR-0047's base,
// so a caller that never scopes sees no change.
func TestNextBaseUnscopedIsTheExport(t *testing.T) {
	got := string(NextBase(storeOf(lines(secondSession...)), headStore(), lines(baseExport...), nil))
	if want := strings.Join(secondSession, "\n") + "\n"; got != want {
		t.Errorf("next base:\n%s\nwant the export:\n%s", got, want)
	}
}
