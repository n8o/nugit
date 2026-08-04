package retrieval

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// Markdown renders the bundle for human inspection (`nugit context`).
func (b Bundle) Markdown() string {
	var w strings.Builder
	comp := b.Component
	if comp == "" {
		comp = "(unmapped — no component owns this path)"
	}
	fmt.Fprintf(&w, "## context(%s) → component **%s**\n\n", b.Path, comp)

	if len(b.C4.DependsOn) > 0 || len(b.C4.DependedOnBy) > 0 {
		w.WriteString("**Architecture slice**\n")
		if len(b.C4.DependsOn) > 0 {
			fmt.Fprintf(&w, "- depends on: %s\n", strings.Join(b.C4.DependsOn, ", "))
		}
		if len(b.C4.DependedOnBy) > 0 {
			fmt.Fprintf(&w, "- depended on by: %s\n", strings.Join(b.C4.DependedOnBy, ", "))
		}
		w.WriteString("\n")
	}

	// The org landscape frames everything below it (ADR-0034): if this path
	// configures infrastructure another repo owns, that is the first thing an
	// agent needs to know — before any lesson about how to change it.
	if len(b.Landscape) > 0 {
		fmt.Fprintf(&w, "**Shared infrastructure** _(org landscape, %s)_\n", landscapeWhere(b.Landscape[0]))
		for _, l := range b.Landscape {
			fmt.Fprintf(&w, "- `%s` %s — owned by **%s**%s\n",
				l.ID, landscapeName(l), l.Owner, ownedHereSuffix(l))
		}
		w.WriteString("\n")
	}

	if b.Spec != nil {
		fmt.Fprintf(&w, "**Active spec** `%s` — %s\n\n", b.Spec.QualifiedID(), b.Spec.Summary)
	}

	// Obligations first among the typed sections (ADR-0033): everything below is
	// what this repo knows; this is what this repo OWES.
	if len(b.Contracts) > 0 {
		w.WriteString("**Contracts** _(cross-repo obligations naming this repo)_\n")
		for _, c := range b.Contracts {
			fmt.Fprintf(&w, "- `%s` (%s) — %s\n", c.QualifiedID(), statusLabel(c), c.Summary)
		}
		w.WriteString("\n")
	}

	if len(b.Decisions) > 0 {
		w.WriteString("**Decisions**\n")
		for _, d := range b.Decisions {
			fmt.Fprintf(&w, "- `%s` (%s) — %s%s\n", d.QualifiedID(), statusLabel(d), d.Summary, viaSuffix(d.Via))
			if d.Rejected != "" {
				fmt.Fprintf(&w, "  - 🚫 rejected: %s\n", oneLine(d.Rejected))
			}
			if len(d.AmendedBy) > 0 {
				fmt.Fprintf(&w, "  - ⚠ partially overridden — read together with %s\n", strings.Join(d.AmendedBy, ", "))
			}
		}
		w.WriteString("\n")
	}

	if len(b.Lessons) > 0 {
		w.WriteString("**Lessons**\n")
		for _, l := range b.Lessons {
			fmt.Fprintf(&w, "- `%s`%s — %s%s\n", l.QualifiedID(), tierSuffix(l), l.Summary, viaSuffix(l.Via))
		}
		w.WriteString("\n")
	}

	if len(b.References) > 0 {
		w.WriteString("**References** _(distilled external sources)_\n")
		for _, r := range b.References {
			fmt.Fprintf(&w, "- `%s`%s — %s%s\n", r.QualifiedID(), tierSuffix(r), r.Summary, viaSuffix(r.Via))
		}
		w.WriteString("\n")
	}

	if len(b.Glossary) > 0 {
		w.WriteString("**Glossary**\n")
		for _, g := range b.Glossary {
			fmt.Fprintf(&w, "%s\n", g)
		}
		w.WriteString("\n")
	}

	if len(b.WorkingMemory) > 0 {
		w.WriteString("**Working memory** _(ephemeral, .nugit-local)_\n")
		for _, m := range b.WorkingMemory {
			fmt.Fprintf(&w, "- %s\n", m)
		}
		w.WriteString("\n")
	}

	if len(b.PathHistory) > 0 {
		w.WriteString("**Recent capture on these paths** _(derived from git history)_\n")
		for _, h := range b.PathHistory {
			fmt.Fprintf(&w, "- `%s` %s\n", h.SHA, oneLine(h.Subject))
			if h.Decision != "" {
				fmt.Fprintf(&w, "  - decision: %s\n", oneLine(h.Decision))
			}
			if h.Learned != "" {
				fmt.Fprintf(&w, "  - learned: %s\n", oneLine(h.Learned))
			}
		}
		w.WriteString("\n")
	}

	// A configured peer that contributed nothing is stated, never silent: the
	// reader must be able to tell "the sibling has no global knowledge" from
	// "the sibling isn't checked out here" (ADR-0032).
	var absent []string
	for _, p := range b.Peers {
		if !p.Reachable {
			absent = append(absent, p.Name)
		}
	}
	if len(absent) > 0 {
		fmt.Fprintf(&w, "_peer store(s) unreachable, contributed nothing: %s_\n\n", strings.Join(absent, ", "))
	}

	fmt.Fprintf(&w, "_~%d/%d tokens", b.EstimatedTokens, b.BudgetTokens)
	if b.Truncated {
		fmt.Fprintf(&w, " · truncated, dropped: %s", strings.Join(b.Dropped, "; "))
	}
	w.WriteString("_\n")
	return w.String()
}

// landscapeWhere says which landscape this came from — local, or a named peer.
// A reader must never mistake another repo's org model for this repo's.
func landscapeWhere(l LandscapeItem) string {
	if l.Origin == "" {
		return "local"
	}
	return "peer " + l.Origin
}

func landscapeName(l LandscapeItem) string {
	if l.Name == "" {
		return ""
	}
	return "**" + l.Name + "**"
}

// ownedHereSuffix distinguishes "you own this" from "someone else owns this" —
// the difference between a note and an instruction to go talk to a team.
func ownedHereSuffix(l LandscapeItem) string {
	if l.OwnedHere {
		return " _(this repo)_"
	}
	return " _(not this repo — coordinate with the owner)_"
}

// tierWord renders an item's trust tier for humans; the proposed tier reads
// "unratified" so a proposed item never prints "proposed · proposed". Falls
// back to a plain proposed marker when no tier derivation ran.
func tierWord(it Item) string {
	if it.Tier == string(model.EvidenceProposed) {
		return "unratified"
	}
	if it.Tier == "" && it.Status == string(model.StatusProposed) {
		return "unratified"
	}
	return it.Tier
}

// tierSuffix marks items on lines that don't carry a full status label
// (lessons, references) with their trust tier and, when the queried path
// matches the item's applies_to_paths, the path-bound marker (ADR-0020).
// Foreign items lead with their origin (ADR-0032).
func tierSuffix(it Item) string {
	var parts []string
	if s := originWord(it); s != "" {
		parts = append(parts, s)
	}
	if t := tierWord(it); t != "" {
		parts = append(parts, t)
	}
	if it.PathBound {
		parts = append(parts, "path-bound")
	}
	if it.SharedSystem != "" {
		parts = append(parts, "shared system "+it.SharedSystem)
	}
	if len(parts) > 0 {
		return " _[" + strings.Join(parts, " · ") + "]_"
	}
	return ""
}

// originWord names the peer an item came from, so no line in a bundle can be
// read as local knowledge when it isn't (ADR-0032). The qualified id already
// carries the namespace; this says what the prefix MEANS.
func originWord(it Item) string {
	if it.Origin == "" {
		return ""
	}
	return "peer " + it.Origin
}

// statusLabel renders an item's status · trust tier, annotated when the
// object is live but partially overridden by amendments (ADR-0015) and when
// the queried path matches its applies_to_paths binding (ADR-0020).
func statusLabel(it Item) string {
	s := it.Status
	if o := originWord(it); o != "" {
		s = o + " · " + s
	}
	if t := tierWord(it); t != "" {
		s += " · " + t
	}
	if it.PathBound {
		s += " · path-bound"
	}
	if it.SharedSystem != "" {
		s += " · shared system " + it.SharedSystem
	}
	if len(it.AmendedBy) == 0 {
		return s
	}
	return s + ", amended by " + strings.Join(it.AmendedBy, ", ")
}

func viaSuffix(via string) string {
	if via == "" {
		return ""
	}
	return " _(via " + via + ")_"
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 180 {
		return s[:180] + "…"
	}
	return s
}
