package retrieval

import (
	"encoding/json"
	"fmt"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/model"
)

// Per-section relevance caps (ADR-0043). A bundle stops when items stop being
// relevant, not when the budget is full: past the first dozen decisions for one
// path, the ordering below has run out of signal (every remaining item is "same
// tier, next id"), so extra items cost the reader attention without telling it
// anything ranked. Twelve decisions with their rejected rationale is ~14k
// characters, which leaves room for the rest of the bundle inside one MCP
// result. Overridable per repo under `retrieval:` in config.yml.
const (
	DefaultMaxDecisions  = 12
	DefaultMaxLessons    = 8
	DefaultMaxReferences = 6
	// maxDroppedListed bounds the Dropped list itself: a bundle that names
	// every cut item by id can outgrow the bundle it describes.
	maxDroppedListed = 20
	// minFitBudget is where fitChars stops shrinking. Below it only the
	// mandatory c4+spec baseline is left to cut, and that is never cut.
	minFitBudget = 200
)

// Relevance tiers, strongest first. An item's rank is tier*2, plus one when a
// task was given and the item matches none of its keywords — so within a tier a
// task-matching item always outranks a non-matching one. Purely ordinal and
// derived from declared structure: deterministic, LLM-free (ADR-0006).
const (
	tierPath      = iota // applies_to_paths binding or org-landscape binding
	tierComponent        // scoped to the queried path's component
	tierContainer        // scoped to the component's parent container
	tierGlobal           // repo-wide
	tierHop              // pulled in by a one-hop relates_to edge
)

type caps struct{ decisions, lessons, references int }

func capsFrom(cfg config.Config) caps {
	c := caps{DefaultMaxDecisions, DefaultMaxLessons, DefaultMaxReferences}
	if v := cfg.Retrieval.MaxDecisions; v > 0 {
		c.decisions = v
	}
	if v := cfg.Retrieval.MaxLessons; v > 0 {
		c.lessons = v
	}
	if v := cfg.Retrieval.MaxReferences; v > 0 {
		c.references = v
	}
	return c
}

// relevanceRank scores a directly in-scope item (lower is more relevant).
func relevanceRank(it Item, o *model.KnowledgeObject, comp, parent string, kw map[string]bool) int {
	tier := tierGlobal
	switch {
	case it.PathBound || it.SharedSystem != "":
		tier = tierPath
	case comp != "" && it.Scope == comp:
		tier = tierComponent
	case parent != "" && it.Scope == parent:
		tier = tierContainer
	}
	return tier*2 + kwMiss(o, kw)
}

// hopRank scores an item pulled in by traversal: always below every direct item.
func hopRank(o *model.KnowledgeObject, kw map[string]bool) int {
	return tierHop*2 + kwMiss(o, kw)
}

func kwMiss(o *model.KnowledgeObject, kw map[string]bool) int {
	if len(kw) > 0 && !matches(o, kw) {
		return 1
	}
	return 0
}

// head returns the first n items of an already-sorted section.
func head(items []Item, n int) []Item {
	if len(items) > n {
		return items[:n]
	}
	return items
}

// capSection keeps the n most relevant items of a sorted section and returns
// how many were omitted. Local still precedes peer (sortItems), so the cap cuts
// peer knowledge first.
func capSection(items []Item, n int) ([]Item, int) {
	if len(items) <= n {
		return items, 0
	}
	return items[:n], len(items) - n
}

// dead reports a known-wrong object: never served as live context, whether it
// is reached directly or through an edge.
func dead(o *model.KnowledgeObject) bool {
	st := effectiveStatus(o)
	return st == model.StatusSuperseded || st == model.StatusInvalidated
}

// finalize writes the human-readable notices and bounds the Dropped list. It
// runs after truncate, so every notice describes the bundle actually returned.
func finalize(b *Bundle) {
	b.Notices = nil
	if b.Clamped {
		b.Notices = append(b.Notices, fmt.Sprintf(
			"budget clamped from %d to %d to fit the MCP output window", b.RequestedBudgetTokens, b.BudgetTokens))
	}
	for _, kind := range []string{"decision", "lesson", "reference"} {
		if n := b.Omitted[kind]; n > 0 {
			b.Notices = append(b.Notices, fmt.Sprintf(
				"%d more in-scope %ss omitted (relevance cap); call with a narrower path or task", n, kind))
		}
	}
	if n := len(b.Dropped); n > maxDroppedListed {
		b.DroppedCount = n
		b.Dropped = append(b.Dropped[:maxDroppedListed-1:maxDroppedListed-1],
			fmt.Sprintf("(+%d more over budget)", n-(maxDroppedListed-1)))
	}
}

// clone copies the bundle deeply enough that truncate on the copy never writes
// into the original's slices.
func clone(b Bundle) Bundle {
	c := b
	c.Dropped = append([]string(nil), b.Dropped...)
	c.Notices = nil
	return c
}

// fitChars applies the budget, and when maxChars > 0 shrinks the budget until
// the bundle's JSON encoding — exactly what the MCP server sends — fits. The
// token estimate is chars/4 of the CONTENT; JSON field names, quoting and
// escaping are not in it, so a size guarantee has to be measured on the bytes,
// not inferred from the estimate (ADR-0043).
func fitChars(b *Bundle, budget, maxChars int) {
	if maxChars <= 0 {
		truncate(b, budget)
		finalize(b)
		return
	}
	snap := *b
	requested := b.RequestedBudgetTokens
	if !b.Clamped {
		requested = budget
	}
	for {
		trial := clone(snap)
		trial.BudgetTokens = budget
		if budget < requested {
			trial.Clamped, trial.RequestedBudgetTokens = true, requested
		}
		truncate(&trial, budget)
		finalize(&trial)
		js, err := json.Marshal(trial)
		if err != nil || len(js) <= maxChars || budget <= minFitBudget {
			trial.Chars = len(js)
			*b = trial
			return
		}
		budget = budget * 4 / 5
		if budget < minFitBudget {
			budget = minFitBudget
		}
	}
}
