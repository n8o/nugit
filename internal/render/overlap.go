package render

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// maxOverlapFiles caps the shared files listed per item. Two PRs that both
// regenerate a lockfile tree share hundreds; the reviewer needs the count and
// the conflicts, not the manifest.
const maxOverlapFiles = 10

// OverlapSummary is the one-line headline of an overlap view (ADR-0065).
func OverlapSummary(v model.OverlapView) string {
	if v.Pairwise && len(v.Items) == 0 {
		if len(v.Pairs) == 0 {
			return fmt.Sprintf("No two pieces of open work overlap (%d checked)", v.Checked)
		}
		return fmt.Sprintf("%s of open work overlap (%d checked)", plural(len(v.Pairs), "pair"), v.Checked)
	}
	if len(v.Items) == 0 {
		return fmt.Sprintf("No open work overlaps this change (%d checked)", v.Checked)
	}
	collide := 0
	for _, it := range v.Items {
		if len(it.Collisions) > 0 || len(it.Conflicts) > 0 {
			collide++
		}
	}
	s := fmt.Sprintf("Open work that overlaps: %d of %d checked", len(v.Items), v.Checked)
	if collide > 0 {
		s += fmt.Sprintf(", %d would collide or conflict", collide)
	}
	if v.Pairwise {
		s += fmt.Sprintf("; %s overlap each other", plural(len(v.Pairs), "pair"))
	}
	return s
}

// OverlapBody renders the items (and pairs) of an overlap view as markdown.
func OverlapBody(v model.OverlapView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "_Read from %s, as this checkout last fetched them. Shown, never judged: none of this is a finding or changes the exit code (ADR-0065)._\n\n", v.Source)
	if len(v.Unread) > 0 {
		fmt.Fprintf(&b, "Not read here, their heads are not in this checkout: %s.\n\n", strings.Join(v.Unread, ", "))
	}
	for _, it := range v.Items {
		b.WriteString(overlapItem(workLabel(it.With), it.Overlap))
	}
	for _, p := range v.Pairs {
		b.WriteString(overlapItem(workLabel(p.A)+" and "+workLabel(p.B), p.Overlap))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// OverlapMarkdown is the standalone report `nugit overlap` prints.
func OverlapMarkdown(v model.OverlapView) string {
	return "### 🔀 " + OverlapSummary(v) + "\n\n" + OverlapBody(v)
}

// overlapOpen says whether the PR view's section starts expanded: when
// anything would collide, conflict, or shares a file.
func overlapOpen(v model.OverlapView) bool {
	for _, it := range v.Items {
		if len(it.Collisions) > 0 || len(it.Files) > 0 {
			return true
		}
	}
	return false
}

func workLabel(w model.OpenWork) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s**", esc(w.Label()))
	if w.PR > 0 {
		fmt.Fprintf(&b, " `%s`", esc(w.Branch))
		if w.Title != "" {
			fmt.Fprintf(&b, " %s", esc(oneLine(w.Title)))
		}
		if w.Draft {
			b.WriteString(" (draft)")
		}
	} else if w.Pushed != "" {
		fmt.Fprintf(&b, " (no open PR, pushed %s)", w.Pushed)
	}
	return b.String()
}

func overlapItem(label string, o model.Overlap) string {
	var b strings.Builder
	icon := ""
	if len(o.Collisions) > 0 || len(o.Conflicts) > 0 {
		icon = "⚠️ "
	}
	fmt.Fprintf(&b, "- %s%s\n", icon, label)
	for _, c := range o.Collisions {
		fmt.Fprintf(&b, "  - %s\n", esc(c))
	}
	if n := len(o.Files); n > 0 {
		var what string
		switch {
		case len(o.Conflicts) > 0:
			what = fmt.Sprintf("conflicts in %d of %s: %s", len(o.Conflicts), plural(n, "shared file"), codeList(o.Conflicts))
			if rest := minusList(o.Files, o.Conflicts); len(rest) > 0 {
				what += "; also both change " + codeList(rest)
			}
		case o.MergeChecked:
			what = fmt.Sprintf("%s, merges cleanly: %s", plural(n, "shared file"), codeList(o.Files))
		default:
			what = fmt.Sprintf("%s: %s", plural(n, "shared file"), codeList(o.Files))
		}
		fmt.Fprintf(&b, "  - %s\n", what)
	}
	if len(o.Components) > 0 {
		fmt.Fprintf(&b, "  - components: %s\n", esc(strings.Join(o.Components, ", ")))
	}
	return b.String()
}

// codeList renders paths as code spans, capped at maxOverlapFiles.
func codeList(paths []string) string {
	shown := paths
	if len(shown) > maxOverlapFiles {
		shown = shown[:maxOverlapFiles]
	}
	parts := make([]string, 0, len(shown)+1)
	for _, p := range shown {
		parts = append(parts, "`"+esc(p)+"`")
	}
	if extra := len(paths) - len(shown); extra > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", extra))
	}
	return strings.Join(parts, ", ")
}

func minusList(all, drop []string) []string {
	d := map[string]bool{}
	for _, x := range drop {
		d[x] = true
	}
	var out []string
	for _, x := range all {
		if !d[x] {
			out = append(out, x)
		}
	}
	return out
}
