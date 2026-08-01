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
	"github.com/n8o/nugit/internal/evidence"
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
	ID     string `json:"id"`
	Type   string `json:"type"`
	Scope  string `json:"scope"`
	Status string `json:"status"`
	// Tier is the derived trust tier (enforced/checked/declared/proposed/
	// stale) — how much of this item nugit mechanically verifies.
	Tier     string `json:"tier,omitempty"`
	Path     string `json:"path"`
	Summary  string `json:"summary"`
	Rejected string `json:"rejected,omitempty"` // the anti-hallucination field
	Via      string `json:"via,omitempty"`      // relates_to edge that pulled it in (one-hop)
	// AmendedBy: this object is live but PARTIALLY overridden — read it together
	// with these ids (ADR-0015).
	AmendedBy []string `json:"amended_by,omitempty"`
	// PathBound: the queried path matches this object's applies_to_paths — a
	// direct file binding (ADR-0020). The object applies here regardless of
	// its scope and ranks with component-scoped items.
	PathBound bool `json:"path_bound,omitempty"`
	// ReinforcedBy: this object was re-confirmed after a recurrence by these
	// ids, which widen its applicability (ADR-0019).
	ReinforcedBy []string `json:"reinforced_by,omitempty"`
	tokens       int
}

// C4Slice is the component + its immediate relationships.
type C4Slice struct {
	Component    string   `json:"component"`
	DependsOn    []string `json:"depends_on"`
	DependedOnBy []string `json:"depended_on_by"`
}

// Bundle is the composed result.
type Bundle struct {
	Path          string   `json:"path"`
	Component     string   `json:"component"`
	C4            C4Slice  `json:"c4_slice"`
	Decisions     []Item   `json:"decisions"`
	Spec          *Item    `json:"spec,omitempty"`
	Lessons       []Item   `json:"lessons"`
	References    []Item   `json:"references,omitempty"` // distilled external sources
	Glossary      []string `json:"glossary"`
	WorkingMemory []string `json:"working_memory,omitempty"` // ephemeral .nugit-local notes
	// PathHistory: recent commits touching the queried path (subject + captured
	// decision:/learned: trailers), derived from git at read time (ADR-0024).
	// Lowest fill priority — it exists to spend budget the typed sections left
	// unused, so it is the first thing dropped when the budget is tight.
	PathHistory     []HistoryEntry `json:"path_history,omitempty"`
	Truncated       bool           `json:"truncated"`
	Dropped         []string       `json:"dropped,omitempty"` // "type id (reason)" — never a silent cut
	EstimatedTokens int            `json:"estimated_tokens"`
	BudgetTokens    int            `json:"budget_tokens"`
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
	// Scope chain: knowledge scoped to the parent CONTAINER also applies to a
	// path owned by a child component ("" when comp is flat or itself a
	// container — flat models are untouched). Resolve may itself return a
	// container id for container-owned paths.
	parent := m.ContainerOf(comp)

	b := Bundle{Path: opt.Path, Component: comp, BudgetTokens: budget}
	b.C4 = c4Slice(m, comp)

	objs, err := knowledge.Load(opt.RepoDir)
	if err != nil {
		return b, err
	}
	// Trust tiers (never authored): the agent reading this bundle sees how much
	// of each item nugit mechanically verifies.
	evidence.Annotate(objs, evidence.Signals{
		Model:   m,
		Enforce: !cfg.C4Warn(),
		Backend: evidence.BackendActive(opt.RepoDir),
	})
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
	var decisions, lessons, references []Item
	var spec *Item
	var glossary []string
	pulled := map[string]bool{}

	for i := range objs {
		o := &objs[i]
		// Direct path binding (ADR-0020): a queried path matching the object's
		// applies_to_paths makes it behave as if component-scoped, regardless
		// of scope:. The binding substitutes for scope, not for task
		// relevance — the keyword filters below still apply where they would
		// for a component-scoped object.
		bound := knowledge.AppliesTo(o, path)
		if !bound && !inScope(o, comp, parent) {
			continue
		}
		switch o.Type {
		case model.KindDecision:
			// Component-scoped decisions always; global ones only when relevant to
			// the task (else every global decision floods every path's bundle) —
			// unless the queried path itself matches the decision's binding.
			if !bound && (o.Scope == "" || o.Scope == "global") && len(kw) > 0 && !matches(o, kw) {
				continue
			}
			it := toItem(o, "")
			it.PathBound = bound
			decisions = append(decisions, it)
			pulled[o.ID] = true
		case model.KindSpec:
			// One spec slot; a ratified spec displaces a proposed placeholder
			// (ADR-0016) but never the other way around.
			if (bound || relevant(o, comp, parent)) &&
				(spec == nil || (spec.Status == string(model.StatusProposed) && o.Status != model.StatusProposed)) {
				if spec != nil {
					delete(pulled, spec.ID)
				}
				it := toItem(o, "")
				it.PathBound = bound
				spec = &it
				pulled[o.ID] = true
			}
		case model.KindLesson:
			if len(kw) == 0 || matches(o, kw) {
				it := toItem(o, "")
				it.PathBound = bound
				lessons = append(lessons, it)
				pulled[o.ID] = true
			}
		case model.KindReference:
			// Same rule as lessons: keyword-matched when a task is given, all
			// in-scope otherwise (the budget truncates, never silently).
			if len(kw) == 0 || matches(o, kw) {
				it := toItem(o, "")
				it.PathBound = bound
				references = append(references, it)
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
			// Proposed stays IN, labeled (ADR-0016): superseded is known-wrong;
			// proposed is merely unratified and often the only recorded why.
			if st := effectiveStatus(tgt); st == model.StatusSuperseded || st == model.StatusInvalidated {
				continue
			}
			via := edge.Relation + ":" + src.ID
			if tgt.Type == model.KindDecision {
				decisions = append(decisions, toItem(tgt, via))
			} else if tgt.Type == model.KindLesson {
				lessons = append(lessons, toItem(tgt, via))
			} else if tgt.Type == model.KindReference {
				references = append(references, toItem(tgt, via))
			}
		}
	}

	// Reverse informs: pass — a reference declares `informs:<id>` on ITSELF (the
	// decision it grounds is immutable and predates it), so forward traversal
	// can't find it. Pull any live reference that informs an object already in
	// the bundle.
	for i := range objs {
		o := &objs[i]
		if o.Type != model.KindReference || pulled[o.ID] {
			continue
		}
		if st := effectiveStatus(o); st == model.StatusSuperseded || st == model.StatusInvalidated {
			continue
		}
		for _, e := range o.RelatesTo {
			edge := knowledge.ParseEdge(e)
			if edge.Relation == "informs" && pulled[edge.Target] {
				references = append(references, toItem(o, "informs:"+edge.Target))
				pulled[o.ID] = true
				break
			}
		}
	}

	sortItems(decisions)
	sortItems(lessons)
	sortItems(references)
	sort.Strings(glossary)
	glossary = dedup(glossary)

	b.Decisions, b.Spec, b.Lessons, b.References, b.Glossary = decisions, spec, lessons, references, glossary
	b.WorkingMemory = workingMemory(opt.RepoDir, comp, kw)
	b.PathHistory = pathHistory(opt.RepoDir, path)
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
	return overlaps(e.Text+" "+strings.Join(e.Keywords, " "), kw)
}

// truncate enforces the token budget by type priority (c4 > spec > decisions >
// lessons > references > glossary > working memory > path history), dropping
// lowest-priority items first and recording each drop — never a silent cut.
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
	b.References = add(b.References, "reference")
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
	// working memory is near-lowest priority (ephemeral scratch)
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
	// path history is the LAST priority (ADR-0024): the budget-fill section
	// only exists because budget remains, so it is dropped first when tight —
	// and its drops are reported like every other kind, never a silent cut.
	var ph []HistoryEntry
	for _, h := range b.PathHistory {
		if used+h.tokens <= budget {
			used += h.tokens
			ph = append(ph, h)
		} else {
			b.Truncated = true
			b.Dropped = append(b.Dropped, "path-history "+h.SHA+" (over budget)")
		}
	}
	b.PathHistory = ph
	b.EstimatedTokens = used
}
