package c4

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// focusCap bounds the focused diagram: 1-hop neighbors are included only while the
// node count stays under this, otherwise the diagram shows just the directly
// changed components + relationship endpoints (so a change touching a hub like the
// shared types doesn't explode into the whole model).
const focusCap = 18

// MermaidDiff renders the C4 delta as a focused Mermaid diagram: the changed
// components + (budget permitting) their one-hop neighbors, with added components
// green, removed dashed-red, changed yellow, added edges bold-green and removed
// edges dashed-red. GitHub renders the fenced block inline. Empty delta -> "".
func MermaidDiff(d model.C4Delta, head model.Model) string {
	if d.Empty() {
		return ""
	}

	state := map[string]int{} // 0 normal, 1 add, 2 rem, 3 chg
	name := map[string]string{}
	for _, c := range head.Components {
		name[c.ID] = label(c)
	}
	for _, ct := range head.Containers {
		name[ct.ID] = containerLabel(ct)
	}
	for _, c := range d.AddedComponents {
		state[c.ID] = 1
		if name[c.ID] == "" {
			name[c.ID] = label(c)
		}
	}
	for _, c := range d.RemovedComponents {
		state[c.ID] = 2
		name[c.ID] = label(c)
	}
	for _, c := range d.ChangedComponents {
		state[c.After.ID] = 3
		name[c.After.ID] = label(c.After)
	}
	// Container adds/removes/changes join the focused diagram as plain nodes
	// (subgraph rendering is a follow-up; the delta must still be visible).
	for _, ct := range d.AddedContainers {
		state[ct.ID] = 1
		if name[ct.ID] == "" {
			name[ct.ID] = containerLabel(ct)
		}
	}
	for _, ct := range d.RemovedContainers {
		state[ct.ID] = 2
		name[ct.ID] = containerLabel(ct)
	}
	for _, ct := range d.ChangedContainers {
		state[ct.After.ID] = 3
		name[ct.After.ID] = containerLabel(ct.After)
	}

	addRel := map[[2]string]bool{}
	remRel := map[[2]string]bool{}
	for _, r := range d.AddedRels {
		addRel[[2]string{r.Src, r.Dst}] = true
	}
	for _, r := range d.RemovedRels {
		remRel[[2]string{r.Src, r.Dst}] = true
	}

	// core = directly changed components + every relationship endpoint touched.
	core := map[string]bool{}
	for id := range state {
		core[id] = true
	}
	mark := func(s, dst string) { core[s] = true; core[dst] = true }
	for _, r := range d.AddedRels {
		mark(r.Src, r.Dst)
	}
	for _, r := range d.RemovedRels {
		mark(r.Src, r.Dst)
	}
	for _, rc := range d.ChangedRels {
		mark(rc.After.Src, rc.After.Dst)
	}

	// expand to one-hop neighbors from the head model, but only if it stays small.
	focus := map[string]bool{}
	for id := range core {
		focus[id] = true
	}
	for _, r := range head.Relationships {
		if core[r.Src] {
			focus[r.Dst] = true
		}
		if core[r.Dst] {
			focus[r.Src] = true
		}
	}
	if len(focus) > focusCap {
		focus = core // too big: directly-changed neighborhood only
	}
	for id := range focus {
		if name[id] == "" {
			name[id] = id
		}
	}

	// Injective original-id -> mermaid-node-id map, so two ids that sanitize to the
	// same token (e.g. "a.b" vs "a_b") don't merge into one node / a self-loop.
	nodeID := injectiveIDs(sortedKeys(focus))

	var b strings.Builder
	b.WriteString("```mermaid\ngraph LR\n")
	for _, id := range sortedKeys(focus) {
		// literal quotes around the already-escaped label (mermaidLabel is the
		// canonical escape) — never %q, which would double backslashes/control chars.
		fmt.Fprintf(&b, "  %s[\"%s\"]%s\n", nodeID[id], mermaidLabel(name[id]), classOf(state[id]))
	}

	type edge struct {
		s, d string
		st   int // 0 normal, 1 add, 2 rem
	}
	var edges []edge
	seen := map[[2]string]bool{}
	for _, r := range head.Relationships {
		if !focus[r.Src] || !focus[r.Dst] {
			continue
		}
		st := 0
		switch {
		case remRel[[2]string{r.Src, r.Dst}]: // removed wins over a stale head edge
			st = 2
		case addRel[[2]string{r.Src, r.Dst}]:
			st = 1
		}
		edges = append(edges, edge{r.Src, r.Dst, st})
		seen[[2]string{r.Src, r.Dst}] = true
	}
	for _, r := range d.RemovedRels { // removed rels aren't in the head model
		if focus[r.Src] && focus[r.Dst] && !seen[[2]string{r.Src, r.Dst}] {
			edges = append(edges, edge{r.Src, r.Dst, 2})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].s != edges[j].s {
			return edges[i].s < edges[j].s
		}
		return edges[i].d < edges[j].d
	})

	var green, red []int
	for i, e := range edges {
		arrow := "-->"
		switch e.st {
		case 1:
			arrow, green = "==>", append(green, i)
		case 2:
			arrow, red = "-.->", append(red, i)
		}
		fmt.Fprintf(&b, "  %s %s %s\n", nodeID[e.s], arrow, nodeID[e.d])
	}

	b.WriteString("  classDef add fill:#e6ffed,stroke:#1a7f37,color:#000;\n")
	b.WriteString("  classDef rem fill:#ffebe9,stroke:#cf222e,color:#000;\n")
	b.WriteString("  classDef chg fill:#fff8c5,stroke:#9a6700,color:#000;\n")
	for _, i := range green {
		fmt.Fprintf(&b, "  linkStyle %d stroke:#1a7f37,stroke-width:2px;\n", i)
	}
	for _, i := range red {
		fmt.Fprintf(&b, "  linkStyle %d stroke:#cf222e,stroke-dasharray:4;\n", i)
	}
	b.WriteString("```\n")
	return b.String()
}

func classOf(state int) string {
	switch state {
	case 1:
		return ":::add"
	case 2:
		return ":::rem"
	case 3:
		return ":::chg"
	}
	return ""
}

// injectiveIDs maps each original component id to a unique, Mermaid-safe node id.
// Ids that sanitize to the same token are disambiguated with a numeric suffix, so
// distinct components never collapse into one node. Deterministic over sorted ids.
func injectiveIDs(ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	used := map[string]bool{}
	for _, id := range ids {
		base := sanitizeID(id)
		u := base
		for i := 2; used[u]; i++ {
			u = base + "_" + strconv.Itoa(i)
		}
		used[u] = true
		out[id] = u
	}
	return out
}

// sanitizeID maps non-[A-Za-z0-9_] runes to '_' (Mermaid node-id charset).
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "n"
	}
	return b.String()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
