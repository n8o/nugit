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

	// One-hop relates_to traversal: pull the "why" linked by in-scope objects.
	for _, src := range append(append([]Item{}, decisions...), lessons...) {
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
	truncate(&b, budget)
	return b, nil
}

// truncate enforces the token budget by type priority (c4 > spec > decisions >
// lessons > glossary), dropping lowest-priority items first and recording each
// drop — never a silent cut.
func truncate(b *Bundle, budget int) {
	used := tokensOf(b.C4.Component) + 20
	if b.Spec != nil {
		used += b.Spec.tokens
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
	b.EstimatedTokens = used
}
