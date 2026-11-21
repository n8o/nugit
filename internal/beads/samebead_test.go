package beads

import (
	"reflect"
	"testing"
)

// weirHead is a committed shard whose lines say what the database says, in
// three spellings bd does not write: an empty dependency list (acme-weir-1), a
// copy of an edge between two other beads (acme-weir-2 carries weir-3's edge
// to weir-1), and edges in another order (acme-weir-3). Hand merges and older
// writers leave all three.
const weirHead = `{"dependencies":[],"dependency_count":0,"dependent_count":1,"id":"acme-weir-1","status":"closed","title":"weir","updated_at":"2026-11-01T00:00:00Z"}
{"dependencies":[{"depends_on_id":"acme-weir-1","issue_id":"acme-weir-3","type":"blocks"}],"dependency_count":0,"dependent_count":1,"id":"acme-weir-2","status":"closed","title":"sluice","updated_at":"2026-11-01T00:00:00Z"}
{"dependencies":[{"depends_on_id":"acme-weir-2","issue_id":"acme-weir-3","type":"blocks"},{"depends_on_id":"acme-weir-1","issue_id":"acme-weir-3","type":"blocks"}],"dependency_count":2,"dependent_count":0,"id":"acme-weir-3","status":"open","title":"spillway","updated_at":"2026-11-01T00:00:00Z"}
`

// weirDB is the same three beads as bd exports them.
var weirDB = []string{
	`{"id":"acme-weir-1","title":"weir","status":"closed","updated_at":"2026-11-01T00:00:00Z","dependency_count":0,"dependent_count":1}`,
	`{"id":"acme-weir-2","title":"sluice","status":"closed","updated_at":"2026-11-01T00:00:00Z","dependency_count":0,"dependent_count":1}`,
	`{"id":"acme-weir-3","title":"spillway","status":"open","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-1","type":"blocks"},{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-2","type":"blocks"}],"dependency_count":2,"dependent_count":0}`,
}

const weirShard = ".beads/plans/acme-weir.jsonl"

func weirStore() Store {
	return fromFiles([]string{weirShard}, map[string][]byte{weirShard: []byte(weirHead)})
}

// A worktree's first export has no base, and the timestamp rules cannot order
// two lines with one clock: the export's line won, so every line spelled
// differently from bd's was rewritten. Lines that say the same thing about a
// bead are the same bead, and HEAD's bytes stay.
func TestMergeNoBaseKeepsHeadsSpellingOfTheSameBead(t *testing.T) {
	for _, scope := range []Scope{nil, {"acme-weir": true}} {
		got, rep := Merge(storeOf(lines(weirDB...)), weirStore(), nil, scope)
		out, err := Normalize(got, true)
		if err != nil {
			t.Fatal(err)
		}
		if string(out[weirShard]) != weirHead {
			t.Errorf("scope %v rewrote the shard:\n%s\nwant HEAD's:\n%s", scope.List(), out[weirShard], weirHead)
		}
		if !reflect.DeepEqual(rep, MergeReport{NoBase: true}) {
			t.Errorf("scope %v: report = %+v", scope.List(), rep)
		}
	}
}

// An edge the bead owns is still an edit: only order and other beads' copies
// are ignored.
func TestMergeNoBaseStillSeesAnEdgeEdit(t *testing.T) {
	cases := map[string]string{
		"an edge added to an empty list": `{"id":"acme-weir-1","title":"weir","status":"closed","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"issue_id":"acme-weir-1","depends_on_id":"acme-weir-9","type":"blocks"}]}`,
		"an edge removed":                `{"id":"acme-weir-3","title":"spillway","status":"open","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-1","type":"blocks"}]}`,
		"an edge with no issue_id":       `{"id":"acme-weir-2","title":"sluice","status":"closed","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"depends_on_id":"acme-weir-9","type":"blocks"}]}`,
	}
	for name, edit := range cases {
		db := map[string]string{}
		for _, l := range weirDB {
			db[ParseJSONL([]byte(l))[0].ID] = l
		}
		id := ParseJSONL([]byte(edit))[0].ID
		db[id] = edit
		got, _ := Merge(storeOf(lines(db["acme-weir-1"], db["acme-weir-2"], db["acme-weir-3"])), weirStore(), nil, nil)
		if it := byID(got)[id]; it.asRead || !same(it.raw, ParseJSONL([]byte(edit))[0].raw) {
			t.Errorf("%s: HEAD's line was kept over the database's edit", name)
		}
	}
}

// Merged field by field, edges compare the same way: order and copies of
// other beads' edges are no change, so they make no conflict.
func TestMergeFieldsComparesTheEdgesABeadOwns(t *testing.T) {
	base := lines(`{"id":"acme-weir-3","title":"spillway","status":"open","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-1","type":"blocks"},{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-2","type":"blocks"}]}`)
	db := lines(`{"id":"acme-weir-3","title":"spillway","status":"closed","updated_at":"2026-11-02T00:00:00Z","dependencies":[{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-2","type":"blocks"},{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-1","type":"blocks"}]}`)
	head := storeOf(lines(`{"id":"acme-weir-3","title":"the spillway","status":"open","updated_at":"2026-11-01T00:00:00Z","dependencies":[{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-1","type":"blocks"},{"issue_id":"acme-weir-4","depends_on_id":"acme-weir-3","type":"blocks"},{"issue_id":"acme-weir-3","depends_on_id":"acme-weir-2","type":"blocks"}]}`))
	got, rep := Merge(storeOf(db), head, base, nil)
	if !reflect.DeepEqual(rep.Merged, []string{"acme-weir-3"}) || len(rep.Conflicts) != 0 {
		t.Errorf("merged = %v, conflicts = %v; want a clean field merge", rep.Merged, rep.Conflicts)
	}
	it := byID(got)["acme-weir-3"]
	if it.Status != "closed" || it.Title != "the spillway" {
		t.Errorf("got status %q title %q, want the database's close and HEAD's title", it.Status, it.Title)
	}
}
