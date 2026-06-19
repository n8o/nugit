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
	"os"
	"path/filepath"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// Detect reports whether repoDir has a Beads store (a .beads/ dir with JSONL).
func Detect(repoDir string) bool {
	ms, _ := filepath.Glob(filepath.Join(repoDir, ".beads", "*.jsonl"))
	return len(ms) > 0
}

// PlanPosition derives the plan delta from the Beads store. The bool is false
// when there is no usable store (so the caller degrades to plan.yml).
func PlanPosition(repoDir string) (model.PlanPosition, bool) {
	ms, _ := filepath.Glob(filepath.Join(repoDir, ".beads", "*.jsonl"))
	if len(ms) == 0 {
		return model.PlanPosition{}, false
	}
	var issues []Issue
	for _, m := range ms {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		issues = append(issues, ParseJSONL(b)...)
	}
	if len(issues) == 0 {
		return model.PlanPosition{}, false
	}
	return compute(issues), true
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
			Status: strings.ToLower(getStr(m, "status", "state")),
			Type:   strings.ToLower(getStr(m, "issue_type", "type", "kind")),
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
	if epicsOnly {
		unit = "epic"
	}
	note := fmt.Sprintf("via Beads — %d %s(s): %d done, %d remaining (forecast)", len(scope), unit, len(done), len(remaining))
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

func getStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
