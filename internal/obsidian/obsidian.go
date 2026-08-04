// Package obsidian makes nugit's knowledge corpus a first-class Obsidian vault.
// The .nugit/ tree is already plain Markdown (with [[wikilink]] cross-refs), so an
// editor like Obsidian edits the git files directly — no sync, no adapter: editing
// a note IS a git change. This generates an INDEX (a "map of content") so opening
// the vault has a navigable home and the graph view connects.
package obsidian

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// Generate loads the knowledge corpus and renders the vault index markdown.
func Generate(repoDir string) (string, error) {
	objs, err := knowledge.Load(repoDir)
	if err != nil {
		return "", err
	}
	return Index(objs), nil
}

// Index renders an Obsidian map-of-content linking every knowledge object by its
// file (which Obsidian resolves natively), grouped by type.
func Index(objs []model.KnowledgeObject) string {
	groups := []struct {
		kind  model.Kind
		title string
	}{
		{model.KindSpec, "Specs"},
		{model.KindDecision, "Decisions"},
		{model.KindLesson, "Lessons"},
		{model.KindReference, "References"},
		{model.KindContract, "Contracts"},
		{model.KindGlossary, "Glossary"},
	}

	var b strings.Builder
	b.WriteString("# nugit knowledge — index\n\n")
	b.WriteString("> Open this folder as an [Obsidian](https://obsidian.md) vault (or any Markdown editor).\n")
	b.WriteString("> These are the same files git tracks — editing a note **is** a git change.\n")
	b.WriteString("> Regenerate this index with `nugit obsidian`.\n\n")

	for _, g := range groups {
		var items []model.KnowledgeObject
		for _, o := range objs {
			if o.Type == g.kind {
				items = append(items, o)
			}
		}
		if len(items) == 0 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		fmt.Fprintf(&b, "## %s\n\n", g.title)
		for _, o := range items {
			fmt.Fprintf(&b, "- [[%s|%s]]\n", noteName(o.Path), label(o))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// noteName is the Obsidian note name for a file (basename without extension).
func noteName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// label is "<id> — <title>" for the link display text.
func label(o model.KnowledgeObject) string {
	t := titleOf(o)
	if t == "" || t == o.ID {
		return o.ID
	}
	return o.ID + " — " + t
}

func titleOf(o model.KnowledgeObject) string {
	for _, line := range strings.Split(o.Body, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "#") {
			s = strings.TrimSpace(strings.TrimLeft(s, "# "))
			// drop a leading "ID — " so the label isn't "ADR-1 — ADR-1 — title"
			if i := strings.Index(s, " — "); i >= 0 && strings.HasPrefix(o.ID, s[:i]) {
				s = strings.TrimSpace(s[i+len(" — "):])
			}
			return s
		}
	}
	return ""
}
