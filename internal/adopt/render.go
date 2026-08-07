package adopt

// The adoption pitch. The report's job is not to list findings, it is to make
// an argument a reader can check: here are two hand-maintained inventories, here
// is where each disagrees with `ls`, and here is how far behind the code each
// one is. Every number is followed by the evidence that produced it.

import (
	"fmt"
	"strings"
)

// maxListed caps each section in the markdown so a repo with fifty unmodeled
// packages produces an argument rather than a wall. The JSON carries everything.
const maxListed = 15

// Markdown renders the human-readable adoption pitch.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# nugit adopt — %s\n\n", r.Repo)
	if r.HasStore {
		b.WriteString("_This repo already has a `.nugit/` store; this report reads the code and the prose only, and ignores it._\n\n")
	} else {
		b.WriteString("_No `.nugit/` here. Everything below was computed from the tree and the repo's own prose — no store, no model, no config._\n\n")
	}

	b.WriteString("## The argument\n\n")
	for _, line := range r.Headlines() {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	b.WriteString("\n")

	if len(r.Docs) > 0 {
		b.WriteString("## Prose inventories, and how far behind they are\n\n")
		b.WriteString("| document | last touched | commits since | unit claims |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, d := range r.Docs {
			last, since := "untracked", "—"
			if !d.Untracked {
				last = shortSHA(d.LastCommit)
				if d.LastDate != "" {
					last += " · " + dateOnly(d.LastDate)
				}
				since = fmt.Sprintf("%d", d.CommitsSince)
				if d.Capped {
					since = "≥" + since
				}
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %d |\n", d.Path, last, since, d.Claims)
		}
		if r.TotalCommits > 0 {
			total := fmt.Sprintf("%d", r.TotalCommits)
			if r.TotalCapped {
				total = "≥" + total
			}
			fmt.Fprintf(&b, "\n_%s commits in this history._\n", total)
		}
		b.WriteString("\n")
	}

	if len(r.Absent) > 0 {
		b.WriteString("## Documented but absent\n\n")
		b.WriteString("Named in prose; no such unit in the tree, and no directory of that name either. A path-shaped claim is listed here only when **this repo's own history** once tracked it — a path this repo never contained is another repo's, not this one's phantom (ADR-0038).\n\n")
		for i, a := range r.Absent {
			if i == maxListed {
				fmt.Fprintf(&b, "\n_…and %d more._\n", len(r.Absent)-maxListed)
				break
			}
			fmt.Fprintf(&b, "- **`%s`** — admitted by `%s`", a.Name, a.Rule)
			if a.DeletedIn != "" {
				fmt.Fprintf(&b, "; this repo deleted `%s` in `%s`", a.DeletedPath, shortSHA(a.DeletedIn))
				if a.DeletedAt != "" {
					fmt.Fprintf(&b, " (%s)", dateOnly(a.DeletedAt))
				}
			}
			if a.Peer != "" {
				fmt.Fprintf(&b, "; peer `%s` has it now", a.Peer)
			}
			b.WriteString("\n")
			for j, m := range a.Mentions {
				if j == 3 {
					fmt.Fprintf(&b, "  - _…%d more mention(s)_\n", len(a.Mentions)-3)
					break
				}
				fmt.Fprintf(&b, "  - `%s:%d` — %s\n", m.Doc, m.Line, m.Text)
			}
		}
		b.WriteString("\n")
	}

	if len(r.Elsewhere) > 0 {
		b.WriteString("## Cited here, but never in this repo\n\n")
		b.WriteString("Named by this repo's prose, absent from its tree, and **never present in its history** — so nugit does not assert these are this repo's phantoms. In an organization of sibling repos that document their shared boundary, this is usually the other repo's code.\n\n")
		for i, e := range r.Elsewhere {
			if i == maxListed {
				fmt.Fprintf(&b, "\n_…and %d more._\n", len(r.Elsewhere)-maxListed)
				break
			}
			if e.Peer != "" {
				fmt.Fprintf(&b, "- **`%s`** — documented here, lives in peer `%s` (`%s`)\n", e.Name, e.Peer, e.PeerPath)
			} else {
				fmt.Fprintf(&b, "- **`%s`** — cited here; no configured peer contains it either\n", e.Name)
			}
			for j, m := range e.Mentions {
				if j == 2 {
					fmt.Fprintf(&b, "  - _…%d more mention(s)_\n", len(e.Mentions)-2)
					break
				}
				fmt.Fprintf(&b, "  - `%s:%d` — %s\n", m.Doc, m.Line, m.Text)
			}
		}
		if len(r.Peers) == 0 {
			b.WriteString("\n_No peers are configured, so nugit could only say where these are **not**. Configure `peers:` (ADR-0032) and it can name the repo that has them._\n")
		}
		b.WriteString("\n")
	}

	if len(r.Undocumented) > 0 {
		b.WriteString("## Present but undocumented\n\n")
		b.WriteString("Detected in the tree; named by no prose inventory, anywhere in their text.\n\n")
		for i, u := range r.Undocumented {
			if i == maxListed {
				fmt.Fprintf(&b, "\n_…and %d more._\n", len(r.Undocumented)-maxListed)
				break
			}
			fmt.Fprintf(&b, "- **`%s`** (`%s`, %s) — %s\n", u.Name, u.Dir, u.Kind, u.Evidence)
		}
		b.WriteString("\n")
	}

	if len(r.Disagreements) > 0 {
		b.WriteString("## Disagreements between documents\n\n")
		b.WriteString("The same unit, given two different values for one **scalar** fact by two documents — at most one of them is right. Set-valued differences (two documents citing different files under one component) are not contradictions and are not reported here.\n\n")
		for i, d := range r.Disagreements {
			if i == maxListed {
				fmt.Fprintf(&b, "\n_…and %d more._\n", len(r.Disagreements)-maxListed)
				break
			}
			fmt.Fprintf(&b, "- **`%s`** — %s\n", d.Unit, d.Attr)
			for _, c := range d.Claims {
				fmt.Fprintf(&b, "  - `%s:%d` says `%s`\n", c.Doc, c.Line, c.Values)
			}
		}
		b.WriteString("\n")
	}

	if len(r.Runbooks) > 0 {
		complete := 0
		for _, rb := range r.Runbooks {
			if rb.Complete() {
				complete++
			}
		}
		b.WriteString("## Runbook candidates\n\n")
		fmt.Fprintf(&b, "%d symptom-keyed document(s); %d carry both an observable symptom and a resolution that can be extracted deterministically. The rest are listed with the gap named — nugit will not invent the missing half (ADR-0028).\n\n", len(r.Runbooks), complete)
		for i, rb := range r.Runbooks {
			if i == maxListed {
				fmt.Fprintf(&b, "\n_…and %d more._\n", len(r.Runbooks)-maxListed)
				break
			}
			mark := "○"
			if rb.Complete() {
				mark = "●"
			}
			fmt.Fprintf(&b, "- %s `%s` — **%s**\n", mark, rb.Path, rb.Title)
			if rb.Trigger != "" {
				fmt.Fprintf(&b, "  - trigger (%s): %s\n", rb.TriggerSource, rb.Trigger)
			}
			if rb.Insight != "" {
				fmt.Fprintf(&b, "  - insight (%s): %s\n", rb.InsightSource, rb.Insight)
			}
			for _, g := range rb.Gaps {
				fmt.Fprintf(&b, "  - gap: %s\n", g)
			}
		}
		b.WriteString("\n")
	}

	if len(r.Written) > 0 {
		b.WriteString("## Written candidates\n\n")
		for _, p := range r.Written {
			fmt.Fprintf(&b, "- `%s` (status: proposed — review, then `nugit ratify`)\n", p)
		}
		b.WriteString("\n")
	}

	if len(r.Notes) > 0 {
		b.WriteString("## What this report could not determine\n\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteString("\n")
	}

	b.WriteString("## What adopting would change\n\n")
	b.WriteString("- `nugit init` derives the unit inventory from the build graph, so it cannot disagree with `ls` — the two documents above already do.\n")
	b.WriteString("- `model-drift` (ADR-0021) turns \"a service nobody documented\" into a PR warning at the moment someone touches it.\n")
	b.WriteString("- The c4↔code check turns the port/path table into something a PR can fail against instead of prose that rots.\n")
	b.WriteString("- Runbooks become lessons retrievable by scope and path via `context()`, instead of files you have to already know about.\n")
	b.WriteString("\nThis report is read-only and always exits 0. It is a report, not a gate.\n")
	return b.String()
}

// Headlines are the report's top-line numbers, in the order that makes the
// argument: what disagrees first, how stale second.
func (r Report) Headlines() []string {
	var out []string
	out = append(out, fmt.Sprintf("**%d code unit(s)** detected by the build/deploy detectors; **%d prose inventor%s** describe them.",
		len(r.Units), len(r.Docs), plural(len(r.Docs), "y", "ies")))
	if n := len(r.Absent); n > 0 {
		out = append(out, fmt.Sprintf("**%d name(s) documented but absent** — the prose describes units the tree does not contain.", n))
	}
	if n := len(r.Elsewhere); n > 0 {
		peered := 0
		for _, e := range r.Elsewhere {
			if e.Peer != "" {
				peered++
			}
		}
		line := fmt.Sprintf("**%d cited path(s) this repo has never contained** — documented here, but not this repo's code.", n)
		if peered > 0 {
			line += fmt.Sprintf(" %d of them live in a configured peer.", peered)
		}
		out = append(out, line)
	}
	if n := len(r.Undocumented); n > 0 {
		out = append(out, fmt.Sprintf("**%d unit(s) present but undocumented** — real code no inventory mentions at all.", n))
	}
	if n := len(r.Disagreements); n > 0 {
		out = append(out, fmt.Sprintf("**%d disagreement(s) between documents** — the same unit given two different values for one scalar fact.", n))
	}
	if d := r.stalest(); d != nil {
		since := fmt.Sprintf("%d", d.CommitsSince)
		if d.Capped {
			since = "at least " + since
		}
		of := ""
		if r.TotalCommits > 0 {
			of = fmt.Sprintf(", of %d in this history", r.TotalCommits)
		}
		out = append(out, fmt.Sprintf("The stalest inventory, `%s`, is **%s commits behind**%s (last touched %s).",
			d.Path, since, of, dateOnly(d.LastDate)))
	}
	if n := len(r.Runbooks); n > 0 {
		out = append(out, fmt.Sprintf("**%d runbook candidate(s)** — lessons in prose, none retrievable by scope or path.", n))
	}
	return out
}

func (r Report) stalest() *Doc {
	var best *Doc
	for i := range r.Docs {
		d := &r.Docs[i]
		if d.Untracked {
			continue
		}
		if best == nil || d.CommitsSince > best.CommitsSince {
			best = d
		}
	}
	if best == nil || best.CommitsSince == 0 {
		return nil
	}
	return best
}

// SummaryLine is the one-line counts summary for stderr.
func (r Report) SummaryLine() string {
	return fmt.Sprintf("adopt: %d units, %d prose inventories, %d documented-but-absent, %d cited-but-never-here, %d present-but-undocumented, %d disagreements, %d runbook candidates\n",
		len(r.Units), len(r.Docs), len(r.Absent), len(r.Elsewhere), len(r.Undocumented), len(r.Disagreements), len(r.Runbooks))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func dateOnly(s string) string {
	if i := strings.Index(s, "T"); i > 0 {
		return s[:i]
	}
	return s
}
