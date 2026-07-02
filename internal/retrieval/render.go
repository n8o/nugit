package retrieval

import (
	"fmt"
	"strings"
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
			fmt.Fprintf(&w, "- `%s` (%s) — %s%s\n", d.ID, d.Status, d.Summary, viaSuffix(d.Via))
			if d.Rejected != "" {
				fmt.Fprintf(&w, "  - 🚫 rejected: %s\n", oneLine(d.Rejected))
			}
		}
		w.WriteString("\n")
	}

	if len(b.Lessons) > 0 {
		w.WriteString("**Lessons**\n")
		for _, l := range b.Lessons {
			fmt.Fprintf(&w, "- `%s` — %s%s\n", l.ID, l.Summary, viaSuffix(l.Via))
		}
		w.WriteString("\n")
	}

	if len(b.References) > 0 {
		w.WriteString("**References** _(distilled external sources)_\n")
		for _, r := range b.References {
			fmt.Fprintf(&w, "- `%s` — %s%s\n", r.ID, r.Summary, viaSuffix(r.Via))
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

	fmt.Fprintf(&w, "_~%d/%d tokens", b.EstimatedTokens, b.BudgetTokens)
	if b.Truncated {
		fmt.Fprintf(&w, " · truncated, dropped: %s", strings.Join(b.Dropped, "; "))
	}
	w.WriteString("_\n")
	return w.String()
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
