// Package beads reads a Beads issue store (the git-tracked .beads/*.jsonl the
// `bd` tool syncs) to source the plan-position delta — completed / current /
// remaining epics on the path to the goal. Deterministic and dependency-free: it
// parses the committed JSONL directly rather than shelling out to `bd`, matching
// nugit's read-from-git discipline. Absent or empty → caller falls back to the
// .nugit/plan.yml stand-in. Field names are matched tolerantly so a schema drift
// in Beads is a one-line change, not a break.
//
// A store holds MANY plans at once. `bd` keeps one database per repository, so
// on a repo where several agents work concurrently every plan any of them is
// executing lands in the same store — and a PR that advances one of them would
// otherwise render all of them as its own position (ADR-0040). Issues are
// therefore grouped into plans, and the caller renders only the plans the PR
// actually moved.
package beads

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
)

// Issue is the subset of a Beads issue nugit reads.
type Issue struct {
	ID     string
	Title  string
	Status string
	Type   string
	// Plan is the plan this step belongs to, resolved by assignPlans: an
	// explicit `plan` field, else the store file it was read from when the
	// store is sharded, else the id family. Never empty once resolved.
	Plan string
	// File is the store file (git-root-relative) the line came from.
	File string
	// Line is the 1-based line number within File — for lint findings only.
	Line int
	// raw keeps every field of the original object so `nugit plan normalize`
	// can rewrite a store without dropping what nugit does not read.
	raw map[string]any
}

// Store is the whole committed plan store at one ref.
type Store struct {
	Issues []Issue
	// Files are the .jsonl paths read, in sorted order.
	Files []string
	// Sharded is true when the store spans more than one file — the layout that
	// keeps concurrent agents out of each other's diffs, and the one where a
	// file IS a plan.
	Sharded bool
	// Stats records what each file contributed. Kept because nugit skips a line
	// it cannot use SILENTLY — a step that vanishes for a missing field looks
	// exactly like a step that was never written, and only the counts tell them
	// apart. `nugit plan check` is the surface for that.
	Stats map[string]FileStat
	// DuplicateIDs are ids that appeared more than once across the store. The
	// reader keeps the last and drops the rest without saying so, so this is
	// collected before the dedup that hides it.
	DuplicateIDs []string
}

// FileStat is one store file's contribution.
type FileStat struct {
	Lines   int // non-blank lines
	Parsed  int // lines that became an issue
	Skipped int // lines nugit dropped (unparseable, or no id AND no title)
}

// Empty reports whether the store contributes no plan steps.
func (s Store) Empty() bool { return len(s.Issues) == 0 }

// Plans returns the plan names in first-appearance order (the file's order is
// the plan's order, so first-seen is the authored order — never sorted).
func (s Store) Plans() []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range s.Issues {
		if it.Plan == "" || seen[it.Plan] {
			continue
		}
		seen[it.Plan] = true
		out = append(out, it.Plan)
	}
	return out
}

// Read loads the committed store at ref (under prefix, the nugit root). Read at
// the reviewed ref — like every other delta — so the report is a pure function
// of (base, head), never the working tree. ok is false when there is no usable
// store (caller degrades to plan.yml).
func Read(repo gitutil.Repo, ref, prefix string) (Store, bool) {
	paths, err := repo.ListTree(ref)
	if err != nil {
		return Store{}, false
	}
	want := prefix + ".beads/"
	var files []string
	for _, p := range paths {
		if strings.HasPrefix(p, want) && strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return Store{}, false
	}
	sort.Strings(files) // stable order across runs
	st := Store{Files: files, Sharded: len(files) > 1, Stats: map[string]FileStat{}}
	for _, p := range files {
		src, e := repo.ShowFile(ref, p)
		if e != nil {
			continue
		}
		issues := ParseJSONL([]byte(src))
		for i := range issues {
			issues[i].File = p
		}
		lines := countLines([]byte(src))
		st.Stats[p] = FileStat{Lines: lines, Parsed: len(issues), Skipped: lines - len(issues)}
		st.Issues = append(st.Issues, issues...)
	}
	st.DuplicateIDs = duplicateIDs(st.Issues)
	st.Issues = dedupByID(st.Issues)
	if len(st.Issues) == 0 {
		return Store{}, false
	}
	assignPlans(&st)
	return st, true
}

func countLines(b []byte) int {
	n := 0
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}

// duplicateIDs reports ids seen more than once, in first-seen order.
func duplicateIDs(in []Issue) []string {
	seen, reported := map[string]bool{}, map[string]bool{}
	var out []string
	for _, it := range in {
		if it.ID == "" {
			continue
		}
		if seen[it.ID] && !reported[it.ID] {
			reported[it.ID] = true
			out = append(out, it.ID)
		}
		seen[it.ID] = true
	}
	return out
}

// PlanPosition derives the whole-store plan delta at ref — every plan, unscoped.
// Kept for callers that genuinely want the repo-wide position; pr-render goes
// through delta.DiffPlan, which scopes to the plans the PR moved.
func PlanPosition(repo gitutil.Repo, ref, prefix string) (model.PlanPosition, bool) {
	st, ok := Read(repo, ref, prefix)
	if !ok {
		return model.PlanPosition{}, false
	}
	return Position(st, nil), true
}

// dedupByID collapses duplicate ids (Beads JSONL is a sync log; last write wins),
// preserving first-seen order for the surviving set.
func dedupByID(in []Issue) []Issue {
	idx := map[string]int{}
	var out []Issue
	for _, it := range in {
		if it.ID == "" {
			out = append(out, it) // untitled-but-keyless already filtered in ParseJSONL
			continue
		}
		if i, ok := idx[it.ID]; ok {
			out[i] = it // last write wins
			continue
		}
		idx[it.ID] = len(out)
		out = append(out, it)
	}
	return out
}

// assignPlans resolves every issue's plan, most explicit signal first:
//
//  1. an authored `plan` field on the line — always wins;
//  2. the store file's stem when the store is SHARDED (.beads/plans/rift.jsonl
//     → "rift"). A sharded store is the layout that stops concurrent agents
//     colliding, and there the file is the plan by construction;
//  3. the id family — the id minus its last dash-separated segment, but only
//     when the id has three or more segments. `bd`'s native ids are
//     `<prefix>-<n>` (two segments, e.g. acme-118), so a two-segment id is one
//     bead that is its own plan; a hand-authored step id (acme-142-2b,
//     acme-rift-16, acme-153-c) carries its plan in everything before the last
//     segment. This is why plan-step ids are worth authoring: the family is
//     what nugit groups by when nothing more explicit exists.
func assignPlans(st *Store) {
	for i := range st.Issues {
		it := &st.Issues[i]
		if it.Plan != "" {
			continue
		}
		if st.Sharded && it.File != "" {
			it.Plan = strings.TrimSuffix(path.Base(it.File), ".jsonl")
			continue
		}
		it.Plan = IDFamily(it.ID)
	}
}

// IDFamily derives the plan family from a bead id. Exported because the rule is
// a contract with whoever names the beads, and `nugit plan check` explains it.
func IDFamily(id string) string {
	segs := strings.Split(id, "-")
	if len(segs) < 3 {
		return id
	}
	return strings.Join(segs[:len(segs)-1], "-")
}

// ParseJSONL parses Beads JSONL (one issue object per line), tolerating field
// name variants. Exported for testing without a `bd` install.
func ParseJSONL(b []byte) []Issue {
	var out []Issue
	for n, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber() // never round-trip a large id or timestamp through float64
		if dec.Decode(&m) != nil {
			continue
		}
		it := Issue{
			ID:     getStr(m, "id", "key"),
			Title:  getStr(m, "title", "summary", "name"),
			Status: norm(getStr(m, "status", "state")),
			Type:   norm(getStr(m, "issue_type", "type", "kind")),
			Plan:   strings.TrimSpace(getStr(m, "plan")),
			Line:   n + 1,
			raw:    m,
		}
		if it.ID == "" && it.Title == "" {
			continue
		}
		out = append(out, it)
	}
	return out
}

func label(it Issue) string {
	if it.Title != "" {
		if it.ID != "" {
			return it.ID + " — " + it.Title
		}
		return it.Title
	}
	return it.ID
}

// IsDone / IsActive expose the reader's status vocabulary. They are exported
// because callers that report ON the store (the CLI, doctor) must classify a
// status exactly as the renderer does, and a second copy of these lists is a
// second answer waiting to disagree with this one.
func IsDone(s string) bool { return isDone(norm(s)) }

// IsActive reports whether a status means "in flight" to this reader.
func IsActive(s string) bool { return isActive(norm(s)) }

func isDone(s string) bool {
	switch s {
	case "closed", "done", "completed", "complete", "resolved", "merged":
		return true
	}
	return false
}

func isActive(s string) bool {
	switch s {
	case "in_progress", "in-progress", "inprogress", "active", "doing", "started", "wip":
		return true
	}
	return false
}

// isOpen reports whether a status is one nugit classifies at all. Anything else
// renders as "remaining" — silently, which is what `nugit plan check` warns about.
func isOpen(s string) bool {
	switch s {
	case "open", "todo", "new", "ready", "backlog", "blocked":
		return true
	}
	return false
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func getStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			if t != "" {
				return t
			}
		case json.Number:
			return t.String()
		case float64: // JSON numbers decode to float64 without UseNumber
			return strings.TrimSuffix(fmt.Sprintf("%v", t), ".0")
		case bool:
			return fmt.Sprintf("%v", t)
		}
	}
	return ""
}
