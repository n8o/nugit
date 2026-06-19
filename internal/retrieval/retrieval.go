// Package retrieval implements context(path): a deterministic, scoped, typed,
// budget-bounded knowledge bundle for an agent operating on a path. This is the
// retrieval half of the thesis (§8) — agents fetch only the typed knowledge
// relevant to the path/task, never the whole store. No LLM, no datastore: a
// pure projection over the in-tree .nugit/ objects + the C4 model, rebuilt from
// git each time (the index is the disposable map below).
package retrieval

import (
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/localmem"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
)

// Options configure a context() call.
type Options struct {
	RepoDir      string
	Path         string // file or dir the agent is operating on
	Task         string // optional task text for keyword matching
	BudgetTokens int    // 0 -> config/default
}

// DefaultBudgetTokens is the hard cap on returned size when unset.
const DefaultBudgetTokens = 4000

// Item is one returned knowledge object (decision / spec / lesson).
type Item struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Scope    string `json:"scope"`
	Status   string `json:"status"`
	Path     string `json:"path"`
	Summary  string `json:"summary"`
	Rejected string `json:"rejected,omitempty"` // the anti-hallucination field
	Via      string `json:"via,omitempty"`      // relates_to edge that pulled it in (one-hop)
	tokens   int
}

// C4Slice is the component + its immediate relationships.
type C4Slice struct {
	Component    string   `json:"component"`
	DependsOn    []string `json:"depends_on"`
	DependedOnBy []string `json:"depended_on_by"`
}

// Bundle is the composed result.
type Bundle struct {
	Path            string   `json:"path"`
	Component       string   `json:"component"`
	C4              C4Slice  `json:"c4_slice"`
	Decisions       []Item   `json:"decisions"`
	Spec            *Item    `json:"spec,omitempty"`
	Lessons         []Item   `json:"lessons"`
	Glossary        []string `json:"glossary"`
	WorkingMemory   []string `json:"working_memory,omitempty"` // ephemeral .nugit-local notes
	Truncated       bool     `json:"truncated"`
	Dropped         []string `json:"dropped,omitempty"` // "type id (reason)" — never a silent cut
	EstimatedTokens int      `json:"estimated_tokens"`
	BudgetTokens    int      `json:"budget_tokens"`
}

// Context composes the bundle for opt.Path.
func Context(opt Options) (Bundle, error) {
	cfg, _ := config.Load(opt.RepoDir)
	budget := opt.BudgetTokens
	if budget <= 0 {
		budget = DefaultBudgetTokens
	}

	dslPath := cfg.C4.DSL
	if dslPath == "" {
		dslPath = ".nugit/architecture/workspace.dsl"
	}
	dslSrc, _ := readFile(opt.RepoDir, dslPath)
	m := c4.Parse(dslSrc)
	mp := mapping.New(m)

	path := strings.TrimPrefix(opt.Path, "./")
	comp := mp.Resolve(path)
	if comp == "" {
		comp = mp.ResolveDir(path) // allow a directory argument
	}

	b := Bundle{Path: opt.Path, Component: comp, BudgetTokens: budget}
	b.C4 = c4Slice(m, comp)

	objs, err := knowledge.Load(opt.RepoDir)
	if err != nil {
		return b, err
	}
	byKey := map[string]*model.KnowledgeObject{}
	for i := range objs {
		if objs[i].ID != "" {
			byKey[objs[i].ID] = &objs[i]
		}
	}

	kw := keywords(opt.Task)

	// In-scope objects: scope == component or "global". Nearer scope (component)
	// is preferred when both a global and a component-scoped object would fill the
	// same slot (handled by stable sort: component-scoped first).
	var decisions, lessons []Item
	var spec *Item
	var glossary []string
	pulled := map[string]bool{}

	for i := range objs {
		o := &objs[i]
		if !inScope(o, comp) {
			continue
		}
		switch o.Type {
		case model.KindDecision:
			// Component-scoped decisions always; global ones only when relevant to
			// the task (else every global decision floods every path's bundle).
			if (o.Scope == "" || o.Scope == "global") && len(kw) > 0 && !matches(o, kw) {
				continue
			}
			decisions = append(decisions, toItem(o, ""))
			pulled[o.ID] = true
		case model.KindSpec:
			if spec == nil && relevant(o, comp, path) {
				it := toItem(o, "")
				spec = &it
				pulled[o.ID] = true
			}
		case model.KindLesson:
			if len(kw) == 0 || matches(o, kw) {
				lessons = append(lessons, toItem(o, ""))
				pulled[o.ID] = true
			}
		case model.KindGlossary:
			glossary = append(glossary, glossaryTerms(o, opt.Task, path)...)
		}
	}

	// One-hop relates_to traversal: pull the "why" linked by in-scope objects
	// (including the active spec — its justifying decision is exactly the "why").
	seeds := append([]Item{}, decisions...)
	seeds = append(seeds, lessons...)
	if spec != nil {
		seeds = append(seeds, *spec)
	}
	for _, src := range seeds {
		o := byKey[src.ID]
		if o == nil {
			continue
		}
		for _, e := range o.RelatesTo {
			edge := knowledge.ParseEdge(e)
			tgt := byKey[edge.Target]
			if tgt == nil || pulled[tgt.ID] {
				continue
			}
			pulled[tgt.ID] = true
			// Never surface a superseded/invalidated rationale as live context.
			if st := effectiveStatus(tgt); st == model.StatusSuperseded || st == model.StatusInvalidated {
				continue
			}
			via := edge.Relation + ":" + src.ID
			if tgt.Type == model.KindDecision {
				decisions = append(decisions, toItem(tgt, via))
			} else if tgt.Type == model.KindLesson {
				lessons = append(lessons, toItem(tgt, via))
			}
		}
	}

	sortItems(decisions)
	sortItems(lessons)
	sort.Strings(glossary)
	glossary = dedup(glossary)

	b.Decisions, b.Spec, b.Lessons, b.Glossary = decisions, spec, lessons, glossary
	b.WorkingMemory = workingMemory(opt.RepoDir, comp, kw)
	truncate(&b, budget)
	return b, nil
}

// workingMemory pulls recent ephemeral .nugit-local notes relevant to the
// component (or global) and, when a task is given, the keywords.
func workingMemory(repoDir, comp string, kw map[string]bool) []string {
	var out []string
	for _, e := range localmem.Recent(repoDir, 20) {
		if e.Scope != "" && e.Scope != comp && e.Scope != "global" {
			continue
		}
		if len(kw) > 0 && !hasKeyword(e, kw) {
			continue
		}
		out = append(out, e.Kind+": "+e.Text)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func hasKeyword(e localmem.Entry, kw map[string]bool) bool {
	// Whole-token match (same tokenization as keywords()), not substring — so "go"
	// doesn't spuriously match "algorithm".
	for tok := range keywords(e.Text + " " + strings.Join(e.Keywords, " ")) {
		if kw[tok] {
			return true
		}
	}
	return false
}

// truncate enforces the token budget by type priority (c4 > spec > decisions >
// lessons > glossary), dropping lowest-priority items first and recording each
// drop — never a silent cut.
func truncate(b *Bundle, budget int) {
	used := tokensOf(b.C4.Component) + 20
	if b.Spec != nil {
		used += b.Spec.tokens
	}
	// The mandatory c4+spec baseline can itself exceed the budget — surface that
	// rather than returning EstimatedTokens > budget with Truncated=false.
	if used > budget {
		b.Truncated = true
		b.Dropped = append(b.Dropped, "(c4+spec baseline exceeds the token budget)")
	}
	add := func(items []Item, kind string) []Item {
		var kept []Item
		for _, it := range items {
			if used+it.tokens <= budget {
				used += it.tokens
				kept = append(kept, it)
			} else {
				b.Truncated = true
				b.Dropped = append(b.Dropped, kind+" "+it.ID+" (over budget)")
			}
		}
		return kept
	}
	b.Decisions = add(b.Decisions, "decision")
	b.Lessons = add(b.Lessons, "lesson")
	// glossary is lowest priority
	var g []string
	for _, t := range b.Glossary {
		tk := tokensOf(t)
		if used+tk <= budget {
			used += tk
			g = append(g, t)
		} else {
			b.Truncated = true
			b.Dropped = append(b.Dropped, "glossary "+firstWord(t)+" (over budget)")
		}
	}
	b.Glossary = g
	// working memory is lowest priority (ephemeral scratch) — dropped first
	var wm []string
	for _, t := range b.WorkingMemory {
		tk := tokensOf(t)
		if used+tk <= budget {
			used += tk
			wm = append(wm, t)
		} else {
			b.Truncated = true
			b.Dropped = append(b.Dropped, "working-memory note (over budget)")
		}
	}
	b.WorkingMemory = wm
	b.EstimatedTokens = used
}
