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

	if b.Spec != nil {
		fmt.Fprintf(&w, "**Active spec** `%s` — %s\n\n", b.Spec.ID, b.Spec.Summary)
	}

	if len(b.Decisions) > 0 {
		w.WriteString("**Decisions**\n")
		for _, d := range b.Decisions {
			fmt.Fprintf(&w, "- `%s` (%s) — %s%s\n", d.ID, statusLabel(d), d.Summary, viaSuffix(d.Via))
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
			fmt.Fprintf(&w, "- `%s`%s — %s%s\n", l.ID, tierSuffix(l), l.Summary, viaSuffix(l.Via))
		}
		w.WriteString("\n")
	}

	if len(b.References) > 0 {
		w.WriteString("**References** _(distilled external sources)_\n")
		for _, r := range b.References {
			fmt.Fprintf(&w, "- `%s`%s — %s%s\n", r.ID, tierSuffix(r), r.Summary, viaSuffix(r.Via))
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

	fmt.Fprintf(&w, "_~%d/%d tokens", b.EstimatedTokens, b.BudgetTokens)
	if b.Truncated {
		fmt.Fprintf(&w, " · truncated, dropped: %s", strings.Join(b.Dropped, "; "))
	}
	w.WriteString("_\n")
	return w.String()
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
func tierSuffix(it Item) string {
	t := tierWord(it)
	if it.PathBound {
		if t != "" {
			t += " · path-bound"
		} else {
			t = "path-bound"
		}
	}
	if t != "" {
		return " _[" + t + "]_"
	}
	return ""
}

// statusLabel renders an item's status · trust tier, annotated when the
// object is live but partially overridden by amendments (ADR-0015) and when
// the queried path matches its applies_to_paths binding (ADR-0020).
func statusLabel(it Item) string {
	s := it.Status
	if t := tierWord(it); t != "" {
		s += " · " + t
	}
	if it.PathBound {
		s += " · path-bound"
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
