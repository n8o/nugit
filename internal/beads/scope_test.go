package beads

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// headShards is the store committed at HEAD, byte for byte, as several writers
// left it. A database behind HEAD exported it once (base) and has not changed
// since. Three things in it are not what nugit's own writer would produce, and
// each is ordinary in a store many sessions write:
//
//   - acme-mesh-1 is in another writer's key order with a raw '>' in it;
//   - acme-mesh-3 carries a copy of acme-mesh-4's edge (issue_id is mesh-4),
//     which bd does not count as mesh-3's dependency, nor twice for mesh-2;
//   - acme-dock-1's dependent_count is stale (dock-2 depends on it), and the
//     dock shard is in hand-merged order (dock-3 before dock-2).
var headShards = map[string]string{
	".beads/plans/acme-rift.jsonl": `{"id":"acme-rift-1","status":"closed","title":"cut the rift","updated_at":"2026-11-10T00:00:00Z"}
{"id":"acme-rift-2","status":"open","title":"seal the rift","updated_at":"2026-11-01T00:00:00Z"}
`,
	".beads/plans/acme-mesh.jsonl": `{"title":"route a -> b","id":"acme-mesh-1","status":"open","updated_at":"2026-11-01T00:00:00Z"}
{"dependency_count":0,"dependent_count":1,"id":"acme-mesh-2","status":"open","title":"mesh core","updated_at":"2026-11-01T00:00:00Z"}
{"dependencies":[{"depends_on_id":"acme-mesh-2","issue_id":"acme-mesh-4","type":"blocks"}],"dependency_count":0,"dependent_count":0,"id":"acme-mesh-3","status":"open","title":"mesh edge","updated_at":"2026-11-01T00:00:00Z"}
{"dependencies":[{"depends_on_id":"acme-mesh-2","issue_id":"acme-mesh-4","type":"blocks"}],"dependency_count":1,"dependent_count":0,"id":"acme-mesh-4","status":"open","title":"mesh leaf","updated_at":"2026-11-01T00:00:00Z"}
`,
	".beads/plans/acme-dock.jsonl": `{"dependency_count":0,"dependent_count":0,"id":"acme-dock-1","status":"open","title":"dock","updated_at":"2026-11-01T00:00:00Z"}
{"id":"acme-dock-3","status":"open","title":"merged by another session since the database imported","updated_at":"2026-11-12T00:00:00Z"}
{"dependencies":[{"depends_on_id":"acme-dock-1","issue_id":"acme-dock-2","type":"blocks"}],"dependency_count":1,"dependent_count":0,"id":"acme-dock-2","status":"open","title":"undock","updated_at":"2026-11-01T00:00:00Z"}
`,
}

// baseExport is the database's previous raw export, in bd's own key order and
// with bd's own (correct) counts. The database is behind HEAD: it never saw
// acme-dock-3, and acme-rift-1 is still open in it.
var baseExport = []string{
	`{"id":"acme-rift-1","title":"cut the rift","status":"open","updated_at":"2026-11-01T00:00:00Z"}`,
	`{"id":"acme-rift-2","title":"seal the rift","status":"open","updated_at":"2026-11-01T00:00:00Z"}`,
	`{"id":"acme-mesh-1","title":"route a -> b","status":"open","updated_at":"2026-11-01T00:00:00Z"}`,
	`{"id":"acme-mesh-2","title":"mesh core","status":"open","updated_at":"2026-11-01T00:00:00Z","dependency_count":0,"dependent_count":1}`,
	`{"id":"acme-mesh-3","title":"mesh edge","status":"open","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"issue_id":"acme-mesh-4","depends_on_id":"acme-mesh-2","type":"blocks"}],"dependency_count":0,"dependent_count":0}`,
	`{"id":"acme-mesh-4","title":"mesh leaf","status":"open","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"issue_id":"acme-mesh-4","depends_on_id":"acme-mesh-2","type":"blocks"}],"dependency_count":1,"dependent_count":0}`,
	`{"id":"acme-dock-1","title":"dock","status":"open","updated_at":"2026-11-01T00:00:00Z","dependency_count":0,"dependent_count":1}`,
	`{"id":"acme-dock-2","title":"undock","status":"open","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"issue_id":"acme-dock-2","depends_on_id":"acme-dock-1","type":"blocks"}],"dependency_count":1,"dependent_count":0}`,
}

func headStore() Store {
	var files []string
	srcs := map[string][]byte{}
	for f, s := range headShards {
		files = append(files, f)
		srcs[f] = []byte(s)
	}
	sort.Strings(files)
	return fromFiles(files, srcs)
}

// exportAndMerge runs what an export script runs: the fresh export, merged
// with HEAD against the base, then split into shards.
func exportAndMerge(t *testing.T, fresh []string, scope Scope) (map[string][]byte, MergeReport) {
	t.Helper()
	got, rep := Merge(storeOf(lines(fresh...)), headStore(), lines(baseExport...), scope)
	out, err := Normalize(got, true)
	if err != nil {
		t.Fatal(err)
	}
	return out, rep
}

func changedShards(out map[string][]byte) []string {
	var changed []string
	for f, b := range out {
		if headShards[f] != string(b) {
			changed = append(changed, f)
		}
	}
	for f := range headShards {
		if _, ok := out[f]; !ok {
			changed = append(changed, f+" (gone)")
		}
	}
	sort.Strings(changed)
	return changed
}

// The incident, reduced: with the base in place and the database untouched
// since it, an export must reproduce HEAD byte for byte. It used to rewrite
// every shard holding a line nugit's encoder would spell differently, or a
// derived count a full recount disagrees with — plans no session touched.
func TestMergeUntouchedDatabaseReproducesHead(t *testing.T) {
	out, rep := exportAndMerge(t, baseExport, nil)
	if c := changedShards(out); len(c) > 0 {
		for _, f := range c {
			t.Logf("%s:\n got  %s\n want %s", f, out[strings.TrimSuffix(f, " (gone)")], headShards[strings.TrimSuffix(f, " (gone)")])
		}
		t.Errorf("an export with no edits rewrote %v", c)
	}
	want := MergeReport{Missing: []string{"acme-dock-3"}, Committed: []string{"acme-rift-1"}}
	if !reflect.DeepEqual(rep, want) {
		t.Errorf("report = %+v\nwant     %+v", rep, want)
	}
}

// secondSession is the shared database after this session closed acme-rift-2
// and another session, working the dock and mesh plans, ran `bd update
// acme-dock-2` and `bd create acme-mesh-5` — none of it exported yet.
var secondSession = func() []string {
	fresh := append([]string(nil), baseExport...)
	fresh[1] = `{"id":"acme-rift-2","title":"seal the rift","status":"closed","updated_at":"2026-11-21T09:00:00Z"}` // this session
	fresh[7] = `{"id":"acme-dock-2","title":"undock","status":"in_progress","updated_at":"2026-11-21T08:00:00Z","dependencies":[{"issue_id":"acme-dock-2","depends_on_id":"acme-dock-1","type":"blocks"}],"dependency_count":1,"dependent_count":0}`
	fresh[3] = `{"id":"acme-mesh-2","title":"mesh core","status":"open","updated_at":"2026-11-01T00:00:00Z","dependency_count":0,"dependent_count":2}`
	return append(fresh, `{"id":"acme-mesh-5","title":"mesh tap","status":"open","updated_at":"2026-11-21T08:30:00Z","dependencies":[{"issue_id":"acme-mesh-5","depends_on_id":"acme-mesh-2","type":"blocks"}],"dependency_count":1,"dependent_count":0}`)
}()

// A base kept per clone records what the DATABASE looked like, not what this
// session did: everything any session changed since the last export looks like
// this session's edit. Unscoped, the other session's work rides along.
func TestMergeUnscopedCarriesAnotherSessionsEdits(t *testing.T) {
	out, _ := exportAndMerge(t, secondSession, nil)
	got := changedShards(out)
	want := []string{".beads/plans/acme-dock.jsonl", ".beads/plans/acme-mesh.jsonl", ".beads/plans/acme-rift.jsonl"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changed = %v, want %v (the per-clone base cannot attribute edits)", got, want)
	}
}

// Scoped to the session's plan, every other plan is HEAD's bytes, and the
// other session's edits are named rather than carried.
func TestMergeScopedLeavesOtherPlansAsHeadHasThem(t *testing.T) {
	out, rep := exportAndMerge(t, secondSession, Scope{"acme-rift": true})
	if c := changedShards(out); !reflect.DeepEqual(c, []string{".beads/plans/acme-rift.jsonl"}) {
		t.Errorf("changed = %v, want only the session's own plan", c)
	}
	rift := string(out[".beads/plans/acme-rift.jsonl"])
	if !strings.Contains(rift, `"id":"acme-rift-1","status":"closed"`) {
		t.Errorf("HEAD's close of acme-rift-1 was reverted:\n%s", rift)
	}
	if !strings.Contains(rift, `"id":"acme-rift-2","status":"closed"`) {
		t.Errorf("this session's close of acme-rift-2 was lost:\n%s", rift)
	}
	want := MergeReport{Committed: []string{"acme-rift-1"}, Outside: []string{"acme-dock-2", "acme-mesh-5"}}
	if !reflect.DeepEqual(rep, want) {
		t.Errorf("report = %+v\nwant     %+v", rep, want)
	}
}

// An edge from the session's plan into another one changes the session's line.
// The other plan's derived count is left as HEAD has it: the scope is a promise
// about which files change, and a count is not worth breaking it for.
func TestMergeScopedEdgeIntoAnotherPlan(t *testing.T) {
	fresh := append([]string(nil), baseExport...)
	fresh[1] = `{"id":"acme-rift-2","title":"seal the rift","status":"open","updated_at":"2026-11-21T09:00:00Z","dependencies":[{"issue_id":"acme-rift-2","depends_on_id":"acme-mesh-2","type":"blocks"}]}`
	fresh[3] = `{"id":"acme-mesh-2","title":"mesh core","status":"open","updated_at":"2026-11-01T00:00:00Z","dependency_count":0,"dependent_count":2}`
	out, rep := exportAndMerge(t, fresh, Scope{"acme-rift": true})
	if c := changedShards(out); !reflect.DeepEqual(c, []string{".beads/plans/acme-rift.jsonl"}) {
		t.Errorf("changed = %v, want only the session's own plan", c)
	}
	if len(rep.Outside) != 0 {
		t.Errorf("a count bd derived is not another session's edit: %v", rep.Outside)
	}
}

// Inside the scope, a derived count moves by the edges this export moved, and
// no further: HEAD's stale count stays stale by the same amount rather than
// being "fixed" in a diff that is supposed to be this session's edits.
func TestMergeCountsMoveByTheEdgesTheExportMoved(t *testing.T) {
	fresh := append([]string(nil), baseExport...)
	fresh = append(fresh, `{"id":"acme-dock-4","title":"redock","status":"open","updated_at":"2026-11-21T09:00:00Z","dependencies":[{"issue_id":"acme-dock-4","depends_on_id":"acme-dock-1","type":"blocks"}],"dependency_count":1,"dependent_count":0}`)
	fresh[6] = `{"id":"acme-dock-1","title":"dock","status":"open","updated_at":"2026-11-01T00:00:00Z","dependency_count":0,"dependent_count":2}`
	out, _ := exportAndMerge(t, fresh, Scope{"acme-dock": true})
	dock := ParseJSONL(out[".beads/plans/acme-dock.jsonl"])
	m := byID(Store{Issues: dock})
	if got := m["acme-dock-1"].Field("dependent_count"); got != "1" {
		t.Errorf("acme-dock-1 dependent_count = %s, want HEAD's 0 plus the one edge added", got)
	}
	if _, ok := m["acme-dock-4"]; !ok {
		t.Error("the bead created in scope was dropped")
	}
	if c := changedShards(out); !reflect.DeepEqual(c, []string{".beads/plans/acme-dock.jsonl"}) {
		t.Errorf("changed = %v", c)
	}
}

func TestPlanOf(t *testing.T) {
	head := headStore()
	fresh := storeOf(lines(append(append([]string(nil), baseExport...),
		`{"id":"acme-118","title":"bd-native, filed this session","status":"open"}`,
		`{"id":"acme-tide-1","title":"a new plan","status":"open","plan":"tide"}`)...))
	cases := []struct {
		id, plan string
		found    bool
	}{
		{"acme-dock-3", "acme-dock", true}, // only on HEAD: its shard
		{"acme-rift-2", "acme-rift", true}, // on both: HEAD's shard first
		{"acme-118", "acme-118", true},     // only in the export, two segments
		{"acme-tide-1", "tide", true},      // an explicit plan field
		{"acme-gone-4", "acme-gone", false},
	}
	for _, c := range cases {
		plan, found := PlanOf(c.id, head, fresh)
		if plan != c.plan || found != c.found {
			t.Errorf("PlanOf(%s) = %q, %v; want %q, %v", c.id, plan, found, c.plan, c.found)
		}
	}
}
