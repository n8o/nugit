package beads

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func TestIDFamily(t *testing.T) {
	cases := map[string]string{
		"acme-142-1":   "acme-142",  // authored plan step
		"acme-142-2b":  "acme-142",  // step with a letter suffix
		"acme-153-c":   "acme-153",  // step keyed only by a letter
		"acme-rift-16": "acme-rift", // slug plan, two-digit step
		"acme-118":     "acme-118",  // bd-native id: its own plan, NOT "acme"
		"nugit-6":      "nugit-6",   // ditto — two segments is never a family
		"solo":         "solo",      // no separator at all
		"":             "",          // absent id
		"a-b-c-1":      "a-b-c",     // deeper slugs keep everything but the step
	}
	for in, want := range cases {
		if got := IDFamily(in); got != want {
			t.Errorf("IDFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

// The rule that matters most: a two-segment bd-native id must not collapse a
// whole repo's beads into one plan named after the issue prefix.
func TestBDNativeIDsStayDistinctPlans(t *testing.T) {
	st := storeOf(ParseJSONL([]byte(strings.Join([]string{
		`{"id":"acme-118","title":"A","issue_type":"epic","status":"closed"}`,
		`{"id":"acme-119","title":"B","issue_type":"epic","status":"open"}`,
	}, "\n"))))
	if got := st.Plans(); len(got) != 2 {
		t.Errorf("plans = %v, want two distinct plans", got)
	}
}

func TestShardedStoreFileIsThePlan(t *testing.T) {
	st := Store{Sharded: true, Files: []string{".beads/plans/rift.jsonl", ".beads/plans/health.jsonl"}}
	st.Issues = []Issue{
		{ID: "acme-142-1", Title: "one", Type: "epic", Status: "closed", File: ".beads/plans/rift.jsonl"},
		{ID: "acme-207-1", Title: "two", Type: "epic", Status: "open", File: ".beads/plans/health.jsonl"},
	}
	assignPlans(&st)
	if st.Issues[0].Plan != "rift" || st.Issues[1].Plan != "health" {
		t.Fatalf("sharded store must take the plan from the file: %+v", st.Issues)
	}
}

func TestExplicitPlanFieldWins(t *testing.T) {
	st := storeOf(ParseJSONL([]byte(
		`{"id":"acme-142-1","title":"x","issue_type":"epic","status":"open","plan":"authored"}`)))
	if st.Issues[0].Plan != "authored" {
		t.Errorf("explicit plan field ignored: %q", st.Issues[0].Plan)
	}
}

// Scoping is the whole point: a store holding several plans must be renderable
// as ONE plan's position, with the others named rather than merged in.
func TestPositionScopedToOnePlan(t *testing.T) {
	st := storeOf(ParseJSONL([]byte(strings.Join([]string{
		`{"id":"p-rift-1","title":"rift one","issue_type":"epic","status":"closed"}`,
		`{"id":"p-rift-2","title":"rift two","issue_type":"epic","status":"in_progress"}`,
		`{"id":"p-flux-1","title":"flux one","issue_type":"epic","status":"in_progress"}`,
		`{"id":"p-flux-2","title":"flux two","issue_type":"epic","status":"open"}`,
	}, "\n"))))

	all := Position(st, nil)
	if all.PlansTotal != 2 || len(all.Tracks) != 2 {
		t.Fatalf("unscoped position should hold both plans: %+v", all)
	}

	only := Position(st, map[string]bool{"p-rift": true})
	if len(only.Tracks) != 1 || only.Tracks[0].Name != "p-rift" {
		t.Fatalf("scoped position tracks = %+v, want only p-rift", only.Tracks)
	}
	if !reflect.DeepEqual(only.Hidden, []string{"p-flux"}) {
		t.Errorf("Hidden = %v, want [p-flux] — other plans must be named, not merged", only.Hidden)
	}
	// The other plan's in-flight step must not appear anywhere in this view.
	joined := strings.Join(append(append([]string{only.Current}, only.Completed...), only.Remaining...), " ")
	if strings.Contains(joined, "flux") {
		t.Errorf("another plan's step leaked into the scoped position: %q", joined)
	}
	if !strings.Contains(only.Note, "p-flux") {
		t.Errorf("note should name what it is not showing: %q", only.Note)
	}
}

// Two plans each with a live step is normal under agent fan-out. The flat
// Current can only hold one, so the rest must stay reachable via the tracks
// rather than being quietly demoted to "remaining" with no trace.
func TestConcurrentCurrentsSurviveInTracks(t *testing.T) {
	st := storeOf(ParseJSONL([]byte(strings.Join([]string{
		`{"id":"p-a-1","title":"a","issue_type":"epic","status":"in_progress"}`,
		`{"id":"p-b-1","title":"b","issue_type":"epic","status":"in_progress"}`,
	}, "\n"))))
	pos := Position(st, nil)
	if len(pos.Tracks) != 2 {
		t.Fatalf("want two tracks, got %+v", pos.Tracks)
	}
	for _, tr := range pos.Tracks {
		if len(tr.Current) != 1 {
			t.Errorf("track %s should carry its own current: %+v", tr.Name, tr)
		}
	}
}

func TestLintCatchesTheSilentFailures(t *testing.T) {
	st := storeOf(ParseJSONL([]byte(strings.Join([]string{
		`{"id":"p-a-1","title":"one","issue_type":"epic","status":"in_progress"}`,
		`{"id":"p-a-2","title":"two","issue_type":"epic","status":"in_progress"}`, // 2 live in one plan
		`{"id":"p-a-3","title":"three","issue_type":"epic","status":"deferred"}`,  // unclassified status
		`{"id":"p-a-4","issue_type":"epic","status":"open"}`,                      // no title
		`{"id":"p-b-1","title":"dup","issue_type":"epic","status":"open"}`,
		`{"id":"p-b-1","title":"dup again","issue_type":"epic","status":"closed"}`, // duplicate id
		`{"id":"p-b-2","title":"a task","issue_type":"task","status":"open"}`,      // not an epic
	}, "\n"))))
	st.DuplicateIDs = []string{"p-b-1"}

	titles := map[string]bool{}
	var worst model.Severity
	for _, f := range Lint(st) {
		titles[f.Title] = true
		if f.Severity == model.SevFail {
			worst = model.SevFail
		}
		if f.Check != CheckName {
			t.Errorf("finding %q has check %q, want %q", f.Title, f.Check, CheckName)
		}
	}
	want := []string{
		"duplicate plan step id: p-b-1",
		"plan p-a has 2 steps in flight at once",
		"1 plan step(s) missing an id or a title",
		"1 plan step(s) carry a status nugit does not classify",
		"1 issue(s) are not epics and do not render as plan steps",
	}
	for _, w := range want {
		if !titles[w] {
			t.Errorf("missing finding %q; got %v", w, keys(titles))
		}
	}
	if worst != model.SevFail {
		t.Error("a duplicate id must be a fail — one of the two steps never renders")
	}
}

// A committed sidecar log is the failure that renders phantom plan steps.
func TestLintFlagsNonStoreFiles(t *testing.T) {
	st := Store{
		Files:   []string{".beads/issues.jsonl", ".beads/interactions.jsonl"},
		Sharded: true,
		Stats: map[string]FileStat{
			".beads/issues.jsonl":       {Lines: 2, Parsed: 2},
			".beads/interactions.jsonl": {Lines: 40, Parsed: 0, Skipped: 40},
		},
	}
	fs := Lint(st)
	found := false
	for _, f := range fs {
		if strings.Contains(f.Title, "interactions.jsonl") {
			found = true
		}
	}
	if !found {
		t.Errorf("a committed non-store .jsonl must be flagged; got %+v", fs)
	}
}

// Normalize is the merge-conflict fix: same steps in, byte-identical bytes out,
// regardless of the order `bd export` happened to emit them in.
func TestNormalizeIsOrderIndependent(t *testing.T) {
	lines := []string{
		`{"id":"p-a-10","title":"ten","issue_type":"epic","status":"open"}`,
		`{"id":"p-a-2","title":"two","issue_type":"epic","status":"open"}`,
		`{"id":"p-b-1","title":"one","issue_type":"epic","status":"open"}`,
	}
	shuffled := []string{lines[2], lines[0], lines[1]}

	norm := func(ls []string) string {
		st := storeOf(ParseJSONL([]byte(strings.Join(ls, "\n"))))
		out, err := Normalize(st, false)
		if err != nil {
			t.Fatal(err)
		}
		return string(out[".beads/issues.jsonl"])
	}
	a, b := norm(lines), norm(shuffled)
	if a != b {
		t.Errorf("normalize is order-dependent — the conflict source survives:\n%s\n---\n%s", a, b)
	}
	// p-a-2 before p-a-10: natural order, so a plan's steps do not interleave.
	if strings.Index(a, "p-a-2") > strings.Index(a, "p-a-10") {
		t.Errorf("steps not naturally ordered:\n%s", a)
	}
}

// Normalizing must never drop a field nugit does not read — the store belongs
// to `bd`, and a rewrite that loses its bookkeeping is a corrupt store.
func TestNormalizePreservesUnknownFields(t *testing.T) {
	src := `{"id":"p-a-1","title":"x","issue_type":"epic","status":"open","close_reason":"PR #1","dependency_count":3,"created_at":"2026-08-16T02:21:50Z"}`
	st := storeOf(ParseJSONL([]byte(src)))
	out, err := Normalize(st, false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out[".beads/issues.jsonl"], &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"close_reason", "dependency_count", "created_at"} {
		if _, ok := got[k]; !ok {
			t.Errorf("normalize dropped %q: %s", k, out[".beads/issues.jsonl"])
		}
	}
	if n, _ := got["dependency_count"].(float64); n != 3 {
		t.Errorf("numeric field mangled: %v", got["dependency_count"])
	}
}

func TestNormalizeSplitsByPlan(t *testing.T) {
	st := storeOf(ParseJSONL([]byte(strings.Join([]string{
		`{"id":"p-a-1","title":"a","issue_type":"epic","status":"open"}`,
		`{"id":"p-b-1","title":"b","issue_type":"epic","status":"open"}`,
	}, "\n"))))
	out, err := Normalize(st, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("split should write one file per plan, got %v", keysOf(out))
	}
	for _, want := range []string{".beads/plans/p-a.jsonl", ".beads/plans/p-b.jsonl"} {
		if _, ok := out[want]; !ok {
			t.Errorf("missing %s; got %v", want, keysOf(out))
		}
	}
}

func TestReadDirWalksSubdirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".beads", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".beads/plans/rift.jsonl", `{"id":"x-1","title":"a","issue_type":"epic","status":"open"}`+"\n")
	write(".beads/plans/flux.jsonl", `{"id":"y-1","title":"b","issue_type":"epic","status":"open"}`+"\n")

	st, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Files) != 2 || !st.Sharded {
		t.Fatalf("expected a sharded two-file store, got %+v", st.Files)
	}
	if got := st.Plans(); len(got) != 2 || got[0] != "flux" {
		t.Errorf("plans = %v, want [flux rift] (file order)", got)
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
