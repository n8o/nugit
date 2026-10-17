package beads

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

// MergeReport says, by bead id, where Merge did not simply take the export.
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
	// NoBase is true when no base was supplied and the timestamp rules
	// decided instead (see Merge).
	NoBase bool
}

// Taken counts the beads where the committed store contributed something.
func (r MergeReport) Taken() int { return len(r.Missing) + len(r.Committed) + len(r.Merged) }

// derived are fields `bd` computes from the dependency graph; Merge ignores
// them when comparing and recomputes them for the merged store.
var derived = map[string]bool{"dependency_count": true, "dependent_count": true}

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
// explicitly, so a merge never moves a line between shard files. Derived
// dependency counts are recomputed over the result.
func Merge(fresh, committed Store, base []Issue) (Store, MergeReport) {
	rep := MergeReport{NoBase: base == nil}
	rIdx := index(committed.Issues)
	bIdx := index(base)
	out := fresh
	out.Issues = nil
	seen := map[string]bool{}
	for _, w := range fresh.Issues {
		r, ok := rIdx[w.ID]
		if w.ID == "" || w.raw == nil || !ok {
			// Created here, or deleted on main. Deleted on main and untouched
			// here is the one case to drop.
			if b, inBase := bIdx[w.ID]; inBase && w.ID != "" && !ok && same(w.raw, b.raw) {
				rep.Deleted = append(rep.Deleted, w.ID)
				continue
			}
			out.Issues = append(out.Issues, w)
			continue
		}
		seen[w.ID] = true
		if getStr(w.raw, "plan") == "" {
			w.Plan = r.Plan
		}
		if same(w.raw, r.raw) {
			out.Issues = append(out.Issues, w)
			continue
		}
		if b, inBase := bIdx[w.ID]; base != nil && inBase {
			switch {
			case same(w.raw, b.raw): // untouched here: main's line
				rep.Committed = append(rep.Committed, w.ID)
				r.Plan = w.Plan
				out.Issues = append(out.Issues, r)
			case same(r.raw, b.raw): // untouched on main: ours
				out.Issues = append(out.Issues, w)
			default:
				m, conflict := mergeFields(b.raw, w.raw, r.raw)
				rep.Merged = append(rep.Merged, w.ID)
				if conflict {
					rep.Conflicts = append(rep.Conflicts, w.ID)
				}
				w.raw = m
				w.Status = norm(getStr(m, "status", "state"))
				w.Title = getStr(m, "title", "summary", "name")
				out.Issues = append(out.Issues, w)
			}
			continue
		}
		if committedWins(w, r) {
			rep.Committed = append(rep.Committed, w.ID)
			r.Plan = w.Plan
			out.Issues = append(out.Issues, r)
			continue
		}
		out.Issues = append(out.Issues, w)
	}
	for _, r := range committed.Issues {
		if r.ID == "" || r.raw == nil || seen[r.ID] {
			continue
		}
		if _, inBase := bIdx[r.ID]; inBase {
			rep.Deleted = append(rep.Deleted, r.ID) // deleted here since the base
			continue
		}
		rep.Missing = append(rep.Missing, r.ID)
		out.Issues = append(out.Issues, r)
	}
	recount(out.Issues)
	for _, s := range []*[]string{&rep.Missing, &rep.Committed, &rep.Merged, &rep.Conflicts, &rep.Deleted} {
		sort.Strings(*s)
	}
	return out, rep
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

// recount rewrites the derived dependency counts from the merged graph, on the
// beads that carry them: after a merge either side's counts can be wrong.
func recount(issues []Issue) {
	dependents := map[string]int{}
	for _, it := range issues {
		for _, d := range deps(it.raw) {
			dependents[getStr(d, "depends_on_id")]++
		}
	}
	for _, it := range issues {
		if it.raw == nil {
			continue
		}
		if _, ok := it.raw["dependency_count"]; ok {
			it.raw["dependency_count"] = json.Number(strconv.Itoa(len(deps(it.raw))))
		}
		if _, ok := it.raw["dependent_count"]; ok {
			it.raw["dependent_count"] = json.Number(strconv.Itoa(dependents[it.ID]))
		}
	}
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
