package c4

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// Mermaid renders a parsed model as a Mermaid `graph LR` for humans (GitHub
// renders it inline). Deterministic: components and edges are sorted.
func Mermaid(m model.Model) string {
	comps := append([]model.Component(nil), m.Components...)
	sort.Slice(comps, func(i, j int) bool { return comps[i].ID < comps[j].ID })
	rels := append([]model.Relationship(nil), m.Relationships...)
	sort.Slice(rels, func(i, j int) bool {
		if rels[i].Src != rels[j].Src {
			return rels[i].Src < rels[j].Src
		}
		return rels[i].Dst < rels[j].Dst
	})

	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, c := range comps {
		fmt.Fprintf(&b, "  %s[%q]\n", c.ID, label(c))
	}
	for _, r := range rels {
		fmt.Fprintf(&b, "  %s --> %s\n", r.Src, r.Dst)
	}
	return b.String()
}

func label(c model.Component) string {
	if c.Name != "" {
		return c.Name
	}
	return c.ID
}
