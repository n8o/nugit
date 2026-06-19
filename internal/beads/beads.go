// Package beads reads a Beads issue store (the git-tracked .beads/*.jsonl the
// `bd` tool syncs) to source the plan-position delta — completed / current /
// remaining epics on the path to the goal. Deterministic and dependency-free: it
// parses the committed JSONL directly rather than shelling out to `bd`, matching
// nugit's read-from-git discipline. Absent or empty → caller falls back to the
// .nugit/plan.yml stand-in. Field names are matched tolerantly so a schema drift
// in Beads is a one-line change, not a break.
package beads

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
)

// PlanPosition derives the plan delta from the Beads store committed at ref
// (under prefix, the nugit root). Read at the reviewed ref — like every other
// delta — so the report is a pure function of (base, head), never the working
// tree. The bool is false when there is no usable store (caller degrades to
// plan.yml).
func PlanPosition(repo gitutil.Repo, ref, prefix string) (model.PlanPosition, bool) {
	paths, err := repo.ListTree(ref)
	if err != nil {
		return model.PlanPosition{}, false
	}
	want := prefix + ".beads/"
	found := false
	var files []string
	for _, p := range paths {
		if strings.HasPrefix(p, want) && strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
			found = true
		}
	}
	if !found {
		return model.PlanPosition{}, false
	}
	sort.Strings(files) // stable order across runs
	var issues []Issue
	for _, p := range files {
		src, e := repo.ShowFile(ref, p)
		if e != nil {
			continue
		}
		issues = append(issues, ParseJSONL([]byte(src))...)
	}
	issues = dedupByID(issues)
	if len(issues) == 0 {
		return model.PlanPosition{}, false
	}
	return compute(issues), true
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

// Issue is the subset of a Beads issue nugit reads.
type Issue struct {
	ID     string
	Title  string
	Status string
	Type   string
}

// ParseJSONL parses Beads JSONL (one issue object per line), tolerating field
// name variants. Exported for testing without a `bd` install.
func ParseJSONL(b []byte) []Issue {
	var out []Issue
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		it := Issue{
			ID:     getStr(m, "id", "key"),
			Title:  getStr(m, "title", "summary", "name"),
			Status: norm(getStr(m, "status", "state")),
			Type:   norm(getStr(m, "issue_type", "type", "kind")),
		}
		if it.ID == "" && it.Title == "" {
			continue
		}
		out = append(out, it)
	}
	return out
}

// compute maps issues to a plan position, preferring epics (the goal-path units)
// and otherwise all issues. Order is preserved (the file's order = the plan's).
func compute(issues []Issue) model.PlanPosition {
	var scope []Issue
	for _, it := range issues {
		if it.Type == "epic" {
			scope = append(scope, it)
		}
	}
	epicsOnly := len(scope) > 0
	if !epicsOnly {
		scope = issues
	}

	var done, remaining []string
	var current string
	for _, it := range scope {
		switch {
		case isDone(it.Status):
			done = append(done, label(it))
		case isActive(it.Status):
			if current == "" {
				current = label(it)
			} else {
				remaining = append(remaining, label(it))
			}
		default:
			remaining = append(remaining, label(it))
		}
	}
	unit := "issue"
	hidden := ""
	if epicsOnly {
		unit = "epic"
		if n := len(issues) - len(scope); n > 0 {
			hidden = fmt.Sprintf(", %d non-epic issue(s) not shown", n)
		}
	}
	note := fmt.Sprintf("via Beads — %d %s(s): %d done, %d remaining%s (forecast)", len(scope), unit, len(done), len(remaining), hidden)
	return model.PlanPosition{
		Present:   true,
		Completed: done,
		Current:   current,
		Remaining: remaining,
		Note:      note,
	}
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
		case float64: // JSON numbers (e.g. a numeric id) decode to float64
			return strings.TrimSuffix(fmt.Sprintf("%v", t), ".0")
		case bool:
			return fmt.Sprintf("%v", t)
		}
	}
	return ""
}
