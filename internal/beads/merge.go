package beads

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

// MergeReport says, by bead id, where Merge did not simply take the export.
// Every list but Outside covers the plans in scope only.
type MergeReport struct {
	// Missing are committed beads the export does not carry and the base never
	// saw: the database never imported them (another session's plan, merged
	// since). Kept as committed.
	Missing []string
	// Committed are beads whose committed line won over the export: the
	// database is behind for them.
	Committed []string
	// Merged are beads both sides changed since the base, combined field by
	// field. Conflicts lists the ones where both changed the SAME field to
	// different values; the export's value was kept.
	Merged    []string
	Conflicts []string
	// Deleted are beads removed from the database since the base (dropped
	// from the store), or removed from the committed store while the database
	// left them untouched.
	Deleted []string
	// Outside are beads in plans outside the scope that an unscoped merge
	// would have changed: edits in the shared database this export did not
	// carry. They are written as committed (ADR-0061).
	Outside []string
	// NoBase is true when no base was supplied and the timestamp rules
	// decided instead (see Merge).
	NoBase bool
}

// Taken counts the beads where the committed store contributed something.
func (r MergeReport) Taken() int { return len(r.Missing) + len(r.Committed) + len(r.Merged) }

// Scope is the set of plans an export may change (ADR-0061). A nil Scope is
// every plan: the unscoped merge of ADR-0047.
type Scope map[string]bool

// Has reports whether the scope includes plan.
func (s Scope) Has(plan string) bool { return s == nil || s[plan] }

// List returns the scope's plans, sorted; nil for every plan.
func (s Scope) List() []string {
	var out []string
	for p := range s {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// PlanOf returns the plan a bead belongs to in the first store that holds it,
// the plan Merge would write it under. found is false when no store holds it;
// plan is then the id's family (IDFamily).
func PlanOf(id string, stores ...Store) (plan string, found bool) {
	for _, st := range stores {
		if it, ok := st.Find(id); ok {
			return it.Plan, true
		}
	}
	return IDFamily(id), false
}

// derived are fields `bd` computes from the dependency graph. Merge ignores
// them when comparing, and moves them by the edges the merge itself moved.
var derived = map[string]bool{"dependency_count": true, "dependent_count": true}

// side says where a merged line came from, which decides what its derived
// counts are relative to.
type side int

const (
	fromHead   side = iota // the committed line
	fromExport             // the export's line
	fromBoth               // merged field by field
)

type pick struct {
	it   Issue
	side side
	// outside marks a committed line written for a plan outside the scope:
	// byte for byte, counts and all.
	outside bool
}

// Merge folds the committed store (read at a ref) into a fresh `bd export`, so
// a database that is behind main cannot revert main (ADR-0047).
//
// The database is a local cache and the committed shards are the source of
// truth, but nothing pulls other sessions' merged changes into the database —
// and `bd export` writes EVERY plan from it. Rewriting the shards from a stale
// database reopens other agents' closes and deletes plans added since the last
// import.
//
// With a base — the previous raw export of this same database — Merge is an
// ordinary three-way merge per bead and, where both sides moved, per field:
// whichever side differs from the base changed it; if both did, the export
// wins and the bead is reported as a conflict. That is exact without any
// timestamps, which matters because `bd` moves no clock for a label or a
// dependency edit. It also makes deletion expressible: in the base and gone
// from the export means deleted here.
//
// Without a base (first run), per bead: only in the export → export; only
// committed → committed (never delete what the database never saw); else the
// later "clock" — max of updated_at, closed_at and its dependencies'
// created_at — wins when both carry updated_at; else a committed close is
// kept over an open export; else the export.
//
// A bead keeps the plan it is committed under unless the export names one
// explicitly, so a merge never moves a line between shard files.
//
// A line the merge takes from the committed store unchanged keeps its bytes
// (Normalize writes it as read), and a derived dependency count moves only by
// the edges this merge moved (ADR-0061). Otherwise an export with no edits
// rewrites every line nugit's encoder would spell differently and every count
// a full recount disagrees with — in plans no session touched.
//
// scope limits which plans the merge may change. A bead whose plan is outside
// it is written as committed, or not at all when it is not committed, whatever
// the database says: the database is shared by every session of the clone,
// and the base cannot tell this session's edits from another's. The beads an
// unscoped merge would have changed there are reported as Outside.
func Merge(fresh, committed Store, base []Issue, scope Scope) (Store, MergeReport) {
	rep := MergeReport{NoBase: base == nil}
	rIdx := index(committed.Issues)
	bIdx := index(base)
	var picks []pick
	seen := map[string]bool{}
	for _, w := range fresh.Issues {
		r, ok := rIdx[w.ID]
		if ok = ok && w.raw != nil; ok {
			seen[w.ID] = true
			if getStr(w.raw, "plan") == "" {
				w.Plan = r.Plan
			}
		}
		p, kept, why := decide(w, r, ok, bIdx, base != nil)
		if !scope.Has(w.Plan) {
			if kept != ok || (kept && !same(p.it.raw, r.raw)) {
				rep.Outside = append(rep.Outside, w.ID)
			}
			if ok {
				picks = append(picks, pick{it: r, side: fromHead, outside: true})
			}
			continue
		}
		for _, list := range why(&rep) {
			*list = append(*list, w.ID)
		}
		if kept {
			picks = append(picks, p)
		}
	}
	for _, r := range committed.Issues {
		if r.ID == "" || r.raw == nil || seen[r.ID] {
			continue
		}
		_, inBase := bIdx[r.ID]
		switch {
		case !scope.Has(r.Plan):
			if inBase {
				rep.Outside = append(rep.Outside, r.ID) // deleted in the database
			}
			picks = append(picks, pick{it: r, side: fromHead, outside: true})
		case inBase:
			rep.Deleted = append(rep.Deleted, r.ID) // deleted here since the base
		default:
			rep.Missing = append(rep.Missing, r.ID)
			picks = append(picks, pick{it: r, side: fromHead})
		}
	}
	out := fresh
	out.Issues = settle(picks, fresh, committed)
	for _, s := range []*[]string{&rep.Missing, &rep.Committed, &rep.Merged, &rep.Conflicts, &rep.Deleted, &rep.Outside} {
		sort.Strings(*s)
	}
	return out, rep
}

// verdict names the report lists a decision lands in, once the caller knows
// the bead is in scope.
type verdict func(*MergeReport) []*[]string

func none(*MergeReport) []*[]string { return nil }

// decide is the per-bead rule of ADR-0047 for an exported bead w, against its
// committed line r (ok is false when there is none). kept is false when the
// bead is dropped.
func decide(w, r Issue, ok bool, bIdx map[string]Issue, haveBase bool) (p pick, kept bool, why verdict) {
	if w.ID == "" || w.raw == nil || !ok {
		// Created here, or deleted on main. Deleted on main and untouched
		// here is the one case to drop.
		if b, inBase := bIdx[w.ID]; inBase && w.ID != "" && w.raw != nil && same(w.raw, b.raw) {
			return pick{}, false, func(m *MergeReport) []*[]string { return []*[]string{&m.Deleted} }
		}
		return pick{it: w, side: fromExport}, true, none
	}
	head := func(why verdict) (pick, bool, verdict) {
		r.Plan = w.Plan
		return pick{it: r, side: fromHead}, true, why
	}
	if same(w.raw, r.raw) {
		return head(none) // the same bead: keep the line as committed
	}
	behind := func(m *MergeReport) []*[]string { return []*[]string{&m.Committed} }
	if b, inBase := bIdx[w.ID]; haveBase && inBase {
		switch {
		case same(w.raw, b.raw): // untouched here: main's line
			return head(behind)
		case same(r.raw, b.raw): // untouched on main: ours
			return pick{it: w, side: fromExport}, true, none
		default:
			m, conflict := mergeFields(b.raw, w.raw, r.raw)
			w.raw = m
			w.Status = norm(getStr(m, "status", "state"))
			w.Title = getStr(m, "title", "summary", "name")
			return pick{it: w, side: fromBoth}, true, func(rp *MergeReport) []*[]string {
				if conflict {
					return []*[]string{&rp.Merged, &rp.Conflicts}
				}
				return []*[]string{&rp.Merged}
			}
		}
	}
	if committedWins(w, r) {
		return head(behind)
	}
	return pick{it: w, side: fromExport}, true, none
}

// mergeFields merges two maps against their base, key by key. A key one side
// left equal to the base takes the other side's value; a key both changed
// differently keeps the export's (w) value and reports a conflict.
func mergeFields(b, w, r map[string]any) (map[string]any, bool) {
	out := map[string]any{}
	conflict := false
	keys := map[string]bool{}
	for _, m := range []map[string]any{b, w, r} {
		for k := range m {
			keys[k] = true
		}
	}
	for k := range keys {
		bv, bok := b[k]
		wv, wok := w[k]
		rv, rok := r[k]
		pick, ok := wv, wok
		switch {
		case eq(wv, wok, rv, rok):
		case eq(wv, wok, bv, bok):
			pick, ok = rv, rok
		case eq(rv, rok, bv, bok):
		default:
			if !derived[k] {
				conflict = true
			}
		}
		if ok {
			out[k] = pick
		}
	}
	return out, conflict
}

func eq(a any, aok bool, b any, bok bool) bool {
	if aok != bok {
		return false
	}
	if !aok {
		return true
	}
	x, e1 := json.Marshal(a)
	y, e2 := json.Marshal(b)
	return e1 == nil && e2 == nil && bytes.Equal(x, y)
}

// committedWins is the no-base rule for a bead both sides carry, differently.
func committedWins(w, r Issue) bool {
	wt, wok := clock(w)
	rt, rok := clock(r)
	if wok && rok && !wt.Equal(rt) {
		return rt.After(wt)
	}
	return isDone(norm(r.Status)) && !isDone(norm(w.Status))
}

// clock is the latest time a bead visibly changed. ok is false without an
// updated_at: closed_at and dependency times alone are too partial to order
// two versions of a bead by.
func clock(it Issue) (time.Time, bool) {
	t, ok := parseTime(getStr(it.raw, "updated_at", "updatedAt"))
	if !ok {
		return time.Time{}, false
	}
	if c, ok := parseTime(getStr(it.raw, "closed_at")); ok && c.After(t) {
		t = c
	}
	for _, d := range deps(it.raw) {
		if c, ok := parseTime(getStr(d, "created_at")); ok && c.After(t) {
			t = c
		}
	}
	return t, true
}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func deps(m map[string]any) []map[string]any {
	list, _ := m["dependencies"].([]any)
	var out []map[string]any
	for _, d := range list {
		if dm, ok := d.(map[string]any); ok {
			out = append(out, dm)
		}
	}
	return out
}

// same compares two lines ignoring the derived counts.
func same(a, b map[string]any) bool {
	return bytes.Equal(canon(a), canon(b))
}

func canon(m map[string]any) []byte {
	c := make(map[string]any, len(m))
	for k, v := range m {
		if !derived[k] {
			c[k] = v
		}
	}
	b, _ := json.Marshal(c)
	return b
}

func index(issues []Issue) map[string]Issue {
	m := map[string]Issue{}
	for _, it := range issues {
		if it.ID != "" && it.raw != nil {
			m[it.ID] = it
		}
	}
	return m
}

// edge is one dependency, identified the way bd counts it: by the bead that
// owns it (issue_id), not by the line it is written on. A line can carry a
// copy of another bead's edge, and bd counts that edge once, for its owner.
type edge struct{ from, to, kind string }

// degrees counts each bead's outgoing (dependency_count) and incoming
// (dependent_count) edges over a set of lines.
type degrees struct{ out, in map[string]int }

func degreesOf(issues []Issue) degrees {
	set := map[edge]bool{}
	for _, it := range issues {
		for _, d := range deps(it.raw) {
			e := edge{getStr(d, "issue_id"), getStr(d, "depends_on_id"), getStr(d, "type")}
			if e.from == "" {
				e.from = it.ID
			}
			if e.to != "" {
				set[e] = true
			}
		}
	}
	g := degrees{out: map[string]int{}, in: map[string]int{}}
	for e := range set {
		g.out[e.from]++
		g.in[e.to]++
	}
	return g
}

// settle turns the picks into the merged store's lines and moves the derived
// counts, on the lines that carry them.
//
// A count moves by the difference the merge made to that bead's edges,
// relative to the store its line came from. It is not recomputed: a committed
// count can already disagree with the committed graph (an edge added by a PR
// that did not rewrite the other bead's line), and "fixing" it in this export
// puts a line in the diff that this session never changed. Only a line merged
// field by field, already in the diff, is recounted outright.
//
// A committed line that ends up unchanged is marked to be written as read.
// A line for a plan outside the scope is never touched.
func settle(picks []pick, fresh, committed Store) []Issue {
	issues := make([]Issue, len(picks))
	for i, p := range picks {
		issues[i] = p.it
	}
	result, head, export := degreesOf(issues), degreesOf(committed.Issues), degreesOf(fresh.Issues)
	for i, p := range picks {
		it := &issues[i]
		if p.outside {
			it.asRead = true
			continue
		}
		from := map[side]*degrees{fromHead: &head, fromExport: &export}[p.side]
		changed := false
		for key, deg := range map[string]func(degrees) map[string]int{
			"dependency_count": func(g degrees) map[string]int { return g.out },
			"dependent_count":  func(g degrees) map[string]int { return g.in },
		} {
			v, has := it.raw[key]
			if !has {
				continue
			}
			want := deg(result)[it.ID]
			if n, err := strconv.Atoi(getStr(it.raw, key)); err == nil && from != nil {
				want = n + deg(result)[it.ID] - deg(*from)[it.ID]
			}
			if num := json.Number(strconv.Itoa(want)); v != num {
				if !changed {
					it.raw = clone(it.raw) // never write through to an input store
					changed = true
				}
				it.raw[key] = num
			}
		}
		it.asRead = p.side == fromHead && !changed
	}
	return issues
}

func clone(m map[string]any) map[string]any {
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
