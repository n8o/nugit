// Package checklist matches checklist items against a diff (ADR-0057).
//
// A checklist item is a knowledge record, written as a lesson, whose
// `diff_trigger:` names paths and, optionally, a pattern for added or removed
// lines, and whose `check:` is the question to ask when it fires. A diff
// usually matches a handful of a repo's items. pr-render lists those, so no
// one has to remember every past incident to know which questions this change
// raises.
//
// Matching is pure over (items, changed files, diff text). The package reads
// no git itself; the engine hands it the range's diff.
package checklist

import (
	"regexp"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// Items selects the live checklist items in objs: records with a diff
// trigger that are neither superseded nor invalidated. A foreign record is an
// item only when it came from the org hub (ADR-0035) and is admissible there
// (global, ratified; knowledge.PeerEligible). Promotion to the hub is how an
// item becomes every repo's, and its paths are then written for any repo.
// Any other peer's paths name files in that peer's tree, so they never fire
// here (ADR-0032).
func Items(objs []model.KnowledgeObject, hub string) []model.KnowledgeObject {
	var out []model.KnowledgeObject
	for i := range objs {
		o := &objs[i]
		if o.DiffTrigger == nil || len(o.DiffTrigger.Paths) == 0 {
			continue
		}
		st := o.EffectiveStatus
		if st == "" {
			st = o.Status
		}
		if st == model.StatusSuperseded || st == model.StatusInvalidated {
			continue
		}
		if o.Foreign() && (hub == "" || o.Origin != hub || !knowledge.PeerEligible(o)) {
			continue
		}
		out = append(out, *o)
	}
	return out
}

// NeedsLines reports whether any item tests line content, so a caller can
// skip computing the diff when every trigger is path-only.
func NeedsLines(items []model.KnowledgeObject) bool {
	for _, o := range items {
		if o.DiffTrigger.Added != "" || o.DiffTrigger.Removed != "" {
			return true
		}
	}
	return false
}

// Candidates returns the changed files some item's paths match, sorted: the
// only files whose diff matching ever needs.
func Candidates(items []model.KnowledgeObject, files []string) []string {
	var out []string
	for _, f := range files {
		for i := range items {
			if knowledge.TriggerCovers(&items[i], f) {
				out = append(out, f)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// Lines is one file's added and removed lines.
type Lines struct {
	Added, Removed []string
}

// Match tests every item against the changed files and their lines (from
// ParseDiff; nil is fine when no item needs lines). An item with an invalid
// pattern never matches; knowledge.InvalidTriggers reports it.
func Match(items []model.KnowledgeObject, files []string, lines map[string]Lines) []model.ChecklistMatch {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	var out []model.ChecklistMatch
	for i := range items {
		o := &items[i]
		t := o.DiffTrigger
		added, aerr := compile(t.Added)
		removed, rerr := compile(t.Removed)
		if aerr != nil || rerr != nil {
			continue
		}
		var m *model.ChecklistMatch
		for _, f := range sorted {
			if !knowledge.TriggerCovers(o, f) {
				continue
			}
			side, line := "path", ""
			if added != nil || removed != nil {
				side, line = lineMatch(lines[f], added, removed)
				if side == "" {
					continue
				}
			}
			if m == nil {
				m = &model.ChecklistMatch{
					ID: o.ID, Origin: o.Origin, Path: o.Path, Title: title(o), Check: strings.TrimSpace(o.Check),
					Status: string(status(o)), File: f, Side: side, Line: line,
				}
			}
			m.Files++
		}
		if m != nil {
			out = append(out, *m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin < out[j].Origin
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func lineMatch(l Lines, added, removed *regexp.Regexp) (side, line string) {
	if added != nil {
		for _, s := range l.Added {
			if added.MatchString(s) {
				return "added", clip(s)
			}
		}
	}
	if removed != nil {
		for _, s := range l.Removed {
			if removed.MatchString(s) {
				return "removed", clip(s)
			}
		}
	}
	return "", ""
}

func compile(p string) (*regexp.Regexp, error) {
	if strings.TrimSpace(p) == "" {
		return nil, nil
	}
	return regexp.Compile(p)
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return s
}

func status(o *model.KnowledgeObject) model.Status {
	if o.EffectiveStatus != "" {
		return o.EffectiveStatus
	}
	return o.Status
}

// title is the record's H1 without its "Lesson — " style prefix.
func title(o *model.KnowledgeObject) string {
	for _, l := range strings.Split(o.Body, "\n") {
		if strings.HasPrefix(l, "# ") {
			t := strings.TrimSpace(strings.TrimPrefix(l, "# "))
			if i := strings.Index(t, " — "); i >= 0 && i < 40 {
				t = t[i+len(" — "):]
			}
			return t
		}
	}
	return o.ID
}

// ParseDiff splits a unified `git diff` into each file's added and removed
// lines, keyed by the file's new path (its old path when deleted). Only lines
// inside a hunk count, so a removed line that begins "--" is never mistaken
// for a header.
func ParseDiff(diff string) map[string]Lines {
	out := map[string]Lines{}
	var cur, oldPath string
	inHunk := false
	for _, raw := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(raw, "diff --git "):
			cur, oldPath, inHunk = "", "", false
			// A change with no ---/+++ lines (binary, mode-only, an empty new
			// file, a pure rename) is still a change to its path: register it
			// from the header, which is unambiguous when both sides agree.
			if p := headerPath(raw[len("diff --git "):]); p != "" {
				if _, ok := out[p]; !ok {
					out[p] = Lines{}
				}
			}
			continue
		case !inHunk && strings.HasPrefix(raw, "rename to "):
			if p := strings.TrimSpace(raw[len("rename to "):]); p != "" {
				if _, ok := out[p]; !ok {
					out[p] = Lines{}
				}
			}
			continue
		case !inHunk && strings.HasPrefix(raw, "--- "):
			oldPath = diffPath(raw[4:], "a/")
			continue
		case !inHunk && strings.HasPrefix(raw, "+++ "):
			cur = diffPath(raw[4:], "b/")
			if cur == "" {
				cur = oldPath // a deletion: +++ /dev/null
			}
			continue
		case strings.HasPrefix(raw, "@@"):
			inHunk = cur != ""
			continue
		}
		if !inHunk || raw == "" {
			continue
		}
		l := out[cur]
		switch raw[0] {
		case '+':
			l.Added = append(l.Added, raw[1:])
		case '-':
			l.Removed = append(l.Removed, raw[1:])
		case ' ', '\\':
		default:
			inHunk = false
			continue
		}
		out[cur] = l
	}
	return out
}

// headerPath reads "a/X b/X" from a diff header when both sides name the same
// path, the only case the header alone settles; a rename is read from its
// "rename to" line instead.
func headerPath(s string) string {
	s = strings.TrimRight(s, "\r")
	if !strings.HasPrefix(s, "a/") || len(s) < 6 || (len(s)-1)%2 != 0 {
		return ""
	}
	half := (len(s) - 1) / 2
	a, sep, b := s[:half], s[half:half+1], s[half+1:]
	if sep != " " || !strings.HasPrefix(b, "b/") || a[2:] != b[2:] {
		return ""
	}
	return a[2:]
}

func diffPath(s, side string) string {
	s = strings.TrimRight(s, "\t\r")
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, side)
}
