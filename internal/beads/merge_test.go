package beads

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// sharded builds a committed store the way the reader sees .beads/plans/: the
// file stem is the plan.
func sharded(files map[string][]string) Store {
	st := Store{Sharded: true, Stats: map[string]FileStat{}}
	for f, lines := range files {
		issues := ParseJSONL([]byte(strings.Join(lines, "\n")))
		for i := range issues {
			issues[i].File = f
		}
		st.Files = append(st.Files, f)
		st.Issues = append(st.Issues, issues...)
	}
	assignPlans(&st)
	return st
}

func byID(st Store) map[string]Issue {
	m := map[string]Issue{}
	for _, it := range st.Issues {
		m[it.ID] = it
	}
	return m
}

// ADR-0047: a database that is behind main must not revert main.
func TestMergeStaleDatabaseCannotRevertMain(t *testing.T) {
	committed := sharded(map[string][]string{
		".beads/plans/acme-rift.jsonl": {
			`{"id":"acme-rift-1","title":"a","status":"closed","updated_at":"2026-10-10T00:00:00Z"}`,
			`{"id":"acme-rift-2","title":"b","status":"open","updated_at":"2026-10-01T00:00:00Z"}`,
		},
		".beads/plans/acme-other.jsonl": {
			`{"id":"acme-other-1","title":"merged by someone else","status":"open","updated_at":"2026-10-12T00:00:00Z"}`,
		},
	})
	fresh := storeOf(ParseJSONL([]byte(strings.Join([]string{
		// stale: closed on main since this database last imported
		`{"id":"acme-rift-1","title":"a","status":"open","updated_at":"2026-10-02T00:00:00Z"}`,
		// this session's edit: newer than main
		`{"id":"acme-rift-2","title":"b","status":"closed","updated_at":"2026-10-16T00:00:00Z"}`,
		// created here
		`{"id":"acme-rift-3","title":"c","status":"open","updated_at":"2026-10-16T00:00:00Z"}`,
	}, "\n"))))

	got, rep := Merge(fresh, committed, nil)
	m := byID(got)
	if m["acme-rift-1"].Status != "closed" {
		t.Errorf("a close on main was reverted by a stale export: %+v", m["acme-rift-1"])
	}
	if m["acme-rift-2"].Status != "closed" {
		t.Errorf("this session's newer edit was lost: %+v", m["acme-rift-2"])
	}
	if _, ok := m["acme-rift-3"]; !ok {
		t.Error("a bead created in this session was dropped")
	}
	if _, ok := m["acme-other-1"]; !ok {
		t.Error("a bead the database never imported was deleted")
	}
	want := MergeReport{Missing: []string{"acme-other-1"}, Committed: []string{"acme-rift-1"}, NoBase: true}
	if !reflect.DeepEqual(rep, want) {
		t.Errorf("report = %+v, want %+v", rep, want)
	}
	if rep.Taken() != 2 {
		t.Errorf("Taken = %d", rep.Taken())
	}
}

// A bead keeps the shard it is committed under, so Normalize writes it back to
// the same file and the diff is the edit, not a move.
func TestMergeKeepsCommittedPlan(t *testing.T) {
	committed := sharded(map[string][]string{
		".beads/plans/rift.jsonl": {`{"id":"acme-118","title":"a","status":"open","updated_at":"2026-10-01T00:00:00Z"}`},
	})
	fresh := storeOf(ParseJSONL([]byte(`{"id":"acme-118","title":"a","status":"closed","updated_at":"2026-10-16T00:00:00Z"}`)))
	got, _ := Merge(fresh, committed, nil)
	out, err := Normalize(got, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[".beads/plans/rift.jsonl"]; !ok || len(out) != 1 {
		t.Errorf("bead moved out of its committed shard: %v", keysOf(out))
	}
	if !strings.Contains(string(out[".beads/plans/rift.jsonl"]), `"status":"closed"`) {
		t.Errorf("the edit did not land: %s", out[".beads/plans/rift.jsonl"])
	}
}

// A store with no timestamps (an export that strips updated_at) still cannot
// have a committed close reverted; any other difference is this session's edit.
func TestMergeWithoutTimestamps(t *testing.T) {
	committed := sharded(map[string][]string{
		".beads/plans/p.jsonl": {
			`{"id":"p-1","title":"a","status":"closed"}`,
			`{"id":"p-2","title":"b","status":"open"}`,
		},
	})
	fresh := storeOf(ParseJSONL([]byte(strings.Join([]string{
		`{"id":"p-1","title":"a","status":"open"}`,
		`{"id":"p-2","title":"b, reworded","status":"open"}`,
	}, "\n"))))
	got, rep := Merge(fresh, committed, nil)
	m := byID(got)
	if m["p-1"].Status != "closed" {
		t.Errorf("committed close reverted: %+v", m["p-1"])
	}
	if m["p-2"].Title != "b, reworded" {
		t.Errorf("this session's edit lost: %+v", m["p-2"])
	}
	if !reflect.DeepEqual(rep.Committed, []string{"p-1"}) {
		t.Errorf("report = %+v", rep)
	}
}

// With timestamps, a deliberate reopen moves updated_at and goes through.
func TestMergeReopenWithTimestampWins(t *testing.T) {
	committed := sharded(map[string][]string{
		".beads/plans/p.jsonl": {`{"id":"p-1","title":"a","status":"closed","updated_at":"2026-10-01T00:00:00Z"}`},
	})
	fresh := storeOf(ParseJSONL([]byte(`{"id":"p-1","title":"a","status":"open","updated_at":"2026-10-16T00:00:00Z"}`)))
	got, rep := Merge(fresh, committed, nil)
	if byID(got)["p-1"].Status != "open" || rep.Taken() != 0 {
		t.Errorf("deliberate reopen blocked: %+v %+v", byID(got)["p-1"], rep)
	}
}

func lines(ls ...string) []Issue { return ParseJSONL([]byte(strings.Join(ls, "\n"))) }

// With a base, the merge is exact without clocks — `bd` moves none for a label
// or dependency edit — and a deletion here is expressible.
func TestMergeThreeWay(t *testing.T) {
	base := lines(
		`{"id":"p-1","title":"a","status":"open","labels":["x"]}`,
		`{"id":"p-2","title":"b","status":"open"}`,
		`{"id":"p-3","title":"c","status":"open"}`,
		`{"id":"p-4","title":"d","status":"open"}`,
		`{"id":"p-5","title":"e","status":"open"}`,
	)
	committed := sharded(map[string][]string{".beads/plans/p.jsonl": {
		`{"id":"p-1","title":"a","status":"closed","labels":["x"]}`, // closed on main
		`{"id":"p-2","title":"b","status":"open"}`,                  // untouched on main
		`{"id":"p-3","title":"c, main","status":"open","labels":["m"]}`,
		`{"id":"p-4","title":"d","status":"open"}`,
		// p-5 deleted on main
		`{"id":"p-6","title":"f","status":"open"}`, // added on main, never imported
	}})
	fresh := storeOf(lines(
		`{"id":"p-1","title":"a","status":"open","labels":["x"]}`,        // stale
		`{"id":"p-2","title":"b","status":"open","labels":["approved"]}`, // label added here, no clock moved
		`{"id":"p-3","title":"c, here","status":"open"}`,                 // both edited title: conflict, ours
		// p-4 deleted here
		`{"id":"p-5","title":"e","status":"open"}`, // untouched here
	))
	got, rep := Merge(fresh, committed, base)
	m := byID(got)
	if m["p-1"].Status != "closed" {
		t.Errorf("stale export reverted main's close: %+v", m["p-1"])
	}
	if l, _ := m["p-2"].raw["labels"].([]any); len(l) != 1 {
		t.Errorf("label added here was lost: %+v", m["p-2"].raw)
	}
	if m["p-3"].Title != "c, here" {
		t.Errorf("conflict should keep the export's value: %+v", m["p-3"])
	}
	if l, _ := m["p-3"].raw["labels"].([]any); len(l) != 1 {
		t.Errorf("main's non-conflicting label should merge in: %+v", m["p-3"].raw)
	}
	for _, id := range []string{"p-4", "p-5"} {
		if _, ok := m[id]; ok {
			t.Errorf("%s should be deleted", id)
		}
	}
	if _, ok := m["p-6"]; !ok {
		t.Error("bead added on main was dropped")
	}
	want := MergeReport{
		Missing: []string{"p-6"}, Committed: []string{"p-1"},
		Merged: []string{"p-3"}, Conflicts: []string{"p-3"}, Deleted: []string{"p-4", "p-5"},
	}
	if !reflect.DeepEqual(rep, want) {
		t.Errorf("report = %+v\nwant     %+v", rep, want)
	}
}

// No base: a dependency added on main moves its own created_at, which is how
// a tie on updated_at is still ordered; the derived counts are recomputed.
func TestMergeDependencyClockAndRecount(t *testing.T) {
	committed := sharded(map[string][]string{".beads/plans/p.jsonl": {
		`{"id":"p-1","title":"a","status":"open","updated_at":"2026-10-01T00:00:00Z","dependency_count":1,"dependent_count":0,"dependencies":[{"issue_id":"p-1","depends_on_id":"p-2","type":"blocks","created_at":"2026-10-05T00:00:00Z"}]}`,
		`{"id":"p-2","title":"b","status":"open","updated_at":"2026-10-01T00:00:00Z","dependency_count":0,"dependent_count":1}`,
	}})
	fresh := storeOf(lines(
		`{"id":"p-1","title":"a","status":"open","updated_at":"2026-10-01T00:00:00Z","dependency_count":0,"dependent_count":0}`,
		`{"id":"p-2","title":"b","status":"open","updated_at":"2026-10-01T00:00:00Z","dependency_count":0,"dependent_count":0}`,
	))
	got, rep := Merge(fresh, committed, nil)
	m := byID(got)
	if len(deps(m["p-1"].raw)) != 1 {
		t.Errorf("main's newer dependency was reverted: %+v", m["p-1"].raw)
	}
	if m["p-2"].raw["dependent_count"].(json.Number) != "1" {
		t.Errorf("dependent_count not recomputed: %+v", m["p-2"].raw)
	}
	if !reflect.DeepEqual(rep.Committed, []string{"p-1"}) {
		t.Errorf("only p-1 differs beyond derived counts, report = %+v", rep)
	}
}
