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
	// SharedSystem names the org-landscape system that admitted this item: the
	// queried path configures a system some OTHER repo owns, and this item is
	// that repo's knowledge about it (ADR-0034). Never silently privileged —
	// the marker rides every rendered line, like path_bound.
	SharedSystem string `json:"shared_system,omitempty"`
	// Origin names the PEER this item came from, or "" for local knowledge
	// (ADR-0032). A reader must never mistake peer knowledge for local
	// knowledge: this repo enforces nothing about a foreign object, its Path
	// names a file in another checkout, and its ID is only unique together with
	// this field.
	Origin string `json:"origin,omitempty"`
	tokens int
}

// QualifiedID is how an item is NAMED anywhere a human or an agent reads it:
// the bare id locally, `<peer>:<id>` for a foreign object (ADR-0032).
func (it Item) QualifiedID() string { return model.QualifyID(it.Origin, it.ID) }

// LandscapeItem is one org-landscape system the queried path configures
// (ADR-0034). It is descriptive, never authored here: the landscape is a single
// artifact owned by one repo in the org (ADR-0011) and read from wherever it
// is authoritative.
type LandscapeItem struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Owner is the repo accountable for the system ("" when it is not shared).
	Owner string `json:"owner,omitempty"`
	// OwnedHere is true when this repo's own `org.repo` is the owner.
	OwnedHere bool `json:"owned_here,omitempty"`
	// Origin is where the LANDSCAPE came from: "" local, else the peer name.
	Origin string `json:"origin,omitempty"`
	// Path is the landscape file the system was read from.
	Path   string `json:"path,omitempty"`
	tokens int
}

// C4Slice is the component + its immediate relationships.
type C4Slice struct {
	Component    string   `json:"component"`
	DependsOn    []string `json:"depends_on"`
	DependedOnBy []string `json:"depended_on_by"`
}

// Bundle is the composed result.
type Bundle struct {
	Path      string  `json:"path"`
	Component string  `json:"component"`
	C4        C4Slice `json:"c4_slice"`
	Decisions []Item  `json:"decisions"`
	Spec      *Item   `json:"spec,omitempty"`
	// Contracts are the ratified cross-repo contracts naming THIS repo as a
	// party (ADR-0033), local or from a peer, each labelled with its origin. An
	// obligation ON this code outranks advice ABOUT it, so they fill right after
	// the single spec slot — which they never displace.
	Contracts []Item `json:"contracts,omitempty"`
	// Landscape are the ORG-level shared systems the queried path configures
	// (ADR-0034), each naming the repo accountable for it. An agent editing a
	// file here must know when the thing it configures belongs to someone else,
	// before it reads a single lesson.
	Landscape     []LandscapeItem `json:"landscape,omitempty"`
	Lessons       []Item          `json:"lessons"`
	References    []Item          `json:"references,omitempty"` // distilled external sources
	Glossary      []string        `json:"glossary"`
	WorkingMemory []string        `json:"working_memory,omitempty"` // ephemeral .nugit-local notes
	// PathHistory: recent commits touching the queried path (subject + captured
	// decision:/learned: trailers), derived from git at read time (ADR-0024).
	// Lowest fill priority — it exists to spend budget the typed sections left
	// unused, so it is the first thing dropped when the budget is tight.
	PathHistory []HistoryEntry `json:"path_history,omitempty"`
	// Peers reports what each configured peer store contributed (ADR-0032),
	// including the ones that contributed nothing because they are not checked
	// out here. Absent peers are made visible, never silently empty.
	Peers           []knowledge.PeerLoad `json:"peers,omitempty"`
	Truncated       bool                 `json:"truncated"`
	Dropped         []string             `json:"dropped,omitempty"` // "type id (reason)" — never a silent cut
	EstimatedTokens int                  `json:"estimated_tokens"`
	BudgetTokens    int                  `json:"budget_tokens"`
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

	// Org landscape (ADR-0034), resolved to exactly one authoritative source
	// (ADR-0011). Entirely inert when no landscape exists anywhere: `land` is
	// zero, `sharedHere` is empty, and every branch below short-circuits.
	land := resolveLandscape(opt.RepoDir, cfg)
	sharedHere := sharedSystems(land, path)
	b.Landscape = landscapeItems(land, sharedHere, cfg.Org.Repo)
	// Which configured peer IS the owner of a shared system this path
	// configures. The bridge is the peer's OWN declared `org.repo`, never its
	// `peers[].name`: the name is this reader's private label (ADR-0033 point 3),
	// while a repo id is the bilateral fact both repos spell identically.
	ownerOf := ownerOrigins(opt.RepoDir, cfg, sharedHere)

	// Local store + every reachable peer (ADR-0032). Only the LOCAL load can
	// error: an absent sibling degrades to "contributed nothing" so pr-render
	// and context() never fail because CI didn't check the peer out.
	objs, peers, err := knowledge.LoadWithPeers(opt.RepoDir, peerSources(opt.RepoDir, cfg))
	if err != nil {
		return b, err
	}
	b.Peers = peers
	// Trust tiers (never authored): the agent reading this bundle sees how much
	// of each item nugit mechanically verifies. A foreign object caps at
	// declared — these signals describe THIS repo's substrate.
	evidence.Annotate(objs, evidence.Signals{
		Model:   m,
		Enforce: !cfg.C4Warn(),
		Backend: evidence.BackendActive(opt.RepoDir),
	})
	// Identity in a merged set is (origin, id), never id: every repo mints
	// ADR-0001, so a bare-id index silently cross-links two stores.
	byKey := map[knowledge.Key]*model.KnowledgeObject{}
	for i := range objs {
		if objs[i].ID != "" {
			byKey[knowledge.KeyOf(objs[i])] = &objs[i]
		}
	}

	kw := keywords(opt.Task)

	// In-scope objects: scope == component or "global". Nearer scope (component)
	// is preferred when both a global and a component-scoped object would fill the
	// same slot (handled by stable sort: component-scoped first).
	var decisions, lessons, references, contracts []Item
	var spec *Item
	var glossary []string
	pulled := map[knowledge.Key]bool{}

	for i := range objs {
		o := &objs[i]
		if o.Foreign() && !peerEligible(o) {
			continue
		}
		// Direct path binding (ADR-0020): a queried path matching the object's
		// applies_to_paths makes it behave as if component-scoped, regardless
		// of scope:. The binding substitutes for scope, not for task
		// relevance — the keyword filters below still apply where they would
		// for a component-scoped object.
		//
		// A FOREIGN object never path-binds: its globs address files in the
		// peer's checkout, and `internal/render/**` matching here would be a
		// coincidence of layout, not a binding (ADR-0032).
		bound := !o.Foreign() && knowledge.AppliesTo(o, path)
		// Landscape binding (ADR-0034): the queried path configures a shared
		// system, and this object comes from the peer that OWNS that system. The
		// glob doing the binding is the ORG's, declared reader-relative in the
		// landscape — not the peer's own applies_to_paths, which still never
		// binds here (ADR-0032 point 6). It is admitted without a keyword match:
		// a declared, bilateral, path-level statement is stronger evidence of
		// relevance than a keyword coincidence, and the set is bounded twice
		// over — by the landscape's globs and by the single owning origin.
		landSys := ""
		if o.Foreign() {
			landSys = ownerOf[o.Origin]
		}
		if !bound && landSys == "" && !inScope(o, comp, parent) {
			continue
		}
		switch o.Type {
		case model.KindDecision:
			// Component-scoped decisions always; global ones only when relevant to
			// the task (else every global decision floods every path's bundle) —
			// unless the queried path itself matches the decision's binding.
			if !bound && landSys == "" && (o.Scope == "" || o.Scope == "global") && len(kw) > 0 && !matches(o, kw) {
				continue
			}
			it := toItem(o, "")
			it.PathBound = bound
			it.SharedSystem = landSys
			decisions = append(decisions, it)
			pulled[knowledge.KeyOf(*o)] = true
		case model.KindSpec:
			// One spec slot; a ratified spec displaces a proposed placeholder
			// (ADR-0016) but never the other way around.
			if (bound || relevant(o, comp, parent)) &&
				(spec == nil || (spec.Status == string(model.StatusProposed) && o.Status != model.StatusProposed)) {
				if spec != nil {
					delete(pulled, itemKey(*spec))
				}
				it := toItem(o, "")
				it.PathBound = bound
				spec = &it
				pulled[knowledge.KeyOf(*o)] = true
			}
		case model.KindLesson:
			if landSys != "" || len(kw) == 0 || matches(o, kw) {
				it := toItem(o, "")
				it.PathBound = bound
				it.SharedSystem = landSys
				lessons = append(lessons, it)
				pulled[knowledge.KeyOf(*o)] = true
			}
		case model.KindReference:
			// Same rule as lessons: keyword-matched when a task is given, all
			// in-scope otherwise (the budget truncates, never silently).
			if landSys != "" || len(kw) == 0 || matches(o, kw) {
				it := toItem(o, "")
				it.PathBound = bound
				it.SharedSystem = landSys
				references = append(references, it)
				pulled[knowledge.KeyOf(*o)] = true
			}
		case model.KindContract:
			// Only a RATIFIED contract that names this repo by its configured
			// `org.repo` (ADR-0033). Deliberately not keyword-gated the way a
			// global decision is: this is not repo-wide advice that could flood
			// a bundle, it is the set of things this repo owes someone — bounded
			// by "contracts that named us", and an agent editing any file here
			// wants to know before it trips one. With no configured identity the
			// section is empty: nugit never guesses which party it is.
			if knowledge.NamesParty(o, cfg.Org.Repo) && knowledge.Ratified(o) {
				it := toItem(o, "")
				it.PathBound = bound
				contracts = append(contracts, it)
				pulled[knowledge.KeyOf(*o)] = true
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
		o := byKey[itemKey(src)]
		if o == nil {
			continue
		}
		for _, e := range o.RelatesTo {
			edge := knowledge.ParseEdge(e)
			// The traversal resolves the edge WITHIN the seed's own store: a
			// peer's `relates_to: [prevents:ADR-0007]` names the peer's
			// ADR-0007, never this repo's (ADR-0032).
			tgt := byKey[knowledge.EdgeKeyFrom(*o, edge.Target)]
			if tgt == nil || pulled[knowledge.KeyOf(*tgt)] {
				continue
			}
			// A foreign target must clear the same peer gate as a foreign seed —
			// traversal is not a back door around global+ratified.
			if tgt.Foreign() && !peerEligible(tgt) {
				continue
			}
			pulled[knowledge.KeyOf(*tgt)] = true
			// Never surface a superseded/invalidated rationale as live context.
			// Proposed stays IN, labeled (ADR-0016): superseded is known-wrong;
			// proposed is merely unratified and often the only recorded why.
			if st := effectiveStatus(tgt); st == model.StatusSuperseded || st == model.StatusInvalidated {
				continue
			}
			via := edge.Relation + ":" + src.QualifiedID()
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
		if o.Type != model.KindReference || pulled[knowledge.KeyOf(*o)] {
			continue
		}
		if st := effectiveStatus(o); st == model.StatusSuperseded || st == model.StatusInvalidated {
			continue
		}
		if o.Foreign() && !peerEligible(o) {
			continue
		}
		for _, e := range o.RelatesTo {
			edge := knowledge.ParseEdge(e)
			// Same-store resolution as the forward pass: a peer reference can
			// only inform an object from its OWN store (ADR-0032).
			if edge.Relation == "informs" && pulled[knowledge.EdgeKeyFrom(*o, edge.Target)] {
				references = append(references, toItem(o, "informs:"+model.QualifyID(o.Origin, edge.Target)))
				pulled[knowledge.KeyOf(*o)] = true
				break
			}
		}
	}

	sortItems(decisions)
	sortItems(lessons)
	sortItems(references)
	sortItems(contracts)
	sort.Strings(glossary)
	glossary = dedup(glossary)

	b.Decisions, b.Spec, b.Lessons, b.References, b.Glossary = decisions, spec, lessons, references, glossary
	b.Contracts = contracts
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

// resolveLandscape picks the ONE authoritative org landscape for this repo's
// view (ADR-0034 point 3 / ADR-0011): this repo's own file if it has one,
// otherwise the single peer that declares one, otherwise nothing. Reading is
// one os.ReadFile, and none happens at all for a repo with no landscape and no
// peers.
func resolveLandscape(repoDir string, cfg config.Config) c4.LandscapeResolution {
	dirs := []c4.LandscapeDir{{Dir: repoDir}}
	for _, p := range cfg.Peers {
		dirs = append(dirs, c4.LandscapeDir{Name: p.Name, Dir: p.Dir(repoDir), Hub: p.Hub})
	}
	return c4.ResolveLandscape(c4.LandscapeSourcesFromDirs(dirs))
}

// sharedSystems are the landscape systems the queried path configures AND that
// declare an owner. A system with no owner is modelled but not shared, so it
// carries no cross-repo consequence.
func sharedSystems(res c4.LandscapeResolution, path string) []model.LandscapeSystem {
	if !res.Found {
		return nil
	}
	var out []model.LandscapeSystem
	for _, s := range c4.Configuring(res.Landscape, path) {
		if s.Shared() {
			out = append(out, s)
		}
	}
	return out
}

func landscapeItems(res c4.LandscapeResolution, shared []model.LandscapeSystem, me string) []LandscapeItem {
	var out []LandscapeItem
	for _, s := range shared {
		it := LandscapeItem{
			ID: s.ID, Name: s.Name, Owner: s.Owner,
			OwnedHere: s.OwnedBy(me),
			Origin:    res.Landscape.Origin, Path: res.Landscape.Path,
		}
		it.tokens = tokensOf(it.Name) + tokensOf(it.ID) + tokensOf(it.Owner) + 8
		out = append(out, it)
	}
	return out
}

// ownerOrigins maps a configured peer's display name onto the shared system it
// OWNS, for the systems this path configures. The join key is the peer's own
// `org.repo`, read from its checkout: a peer's `name` is this reader's private
// label and could never carry a bilateral fact (ADR-0033 point 3). A peer that
// declares no identity, or is not checked out, simply never matches.
func ownerOrigins(repoDir string, cfg config.Config, shared []model.LandscapeSystem) map[string]string {
	if len(shared) == 0 || len(cfg.Peers) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, p := range cfg.Peers {
		pc, err := config.Load(p.Dir(repoDir))
		if err != nil || pc.Org.Repo == "" {
			continue
		}
		for _, s := range shared {
			if s.Owner == pc.Org.Repo {
				out[p.Name] = s.ID
				break
			}
		}
	}
	return out
}

// peerSources maps the configured peers onto the loader's input, resolving each
// peer path against the nugit root.
func peerSources(repoDir string, cfg config.Config) []knowledge.PeerSource {
	if len(cfg.Peers) == 0 {
		return nil
	}
	out := make([]knowledge.PeerSource, 0, len(cfg.Peers))
	for _, p := range cfg.Peers {
		out = append(out, knowledge.PeerSource{Name: p.Name, Dir: p.Dir(repoDir)})
	}
	return out
}

// peerEligible is the admission rule for FOREIGN knowledge — global + ratified
// + decision/lesson/reference/contract. The rule lives in `knowledge` so the
// bundle and the PR-time obligation check can never drift apart about what a
// peer is allowed to say here (ADR-0032, extended for contracts by ADR-0033).
func peerEligible(o *model.KnowledgeObject) bool { return knowledge.PeerEligible(o) }

// itemKey is an item's (origin, id) identity, matching knowledge.KeyOf for the
// object it was built from.
func itemKey(it Item) knowledge.Key { return knowledge.Key{Origin: it.Origin, ID: it.ID} }

// splitOrigin partitions a sorted item slice into its local and foreign runs.
// sortItems already ranks local before peer, so the two runs concatenate back
// into the original order — the split reorders nothing, it only lets the budget
// spend on local knowledge first.
func splitOrigin(items []Item) (local, peer []Item) {
	for _, it := range items {
		if it.Origin == "" {
			local = append(local, it)
		} else {
			peer = append(peer, it)
		}
	}
	return local, peer
}

// truncate enforces the token budget by priority: c4 > spec > CONTRACTS >
// local decisions > local lessons > local references > PEER decisions > peer
// lessons > peer references > glossary > working memory > path history. Peer
// knowledge sits at the bottom of the typed ladder — dropped before any local
// item, including local items of a lower kind — because it is context from
// another repo and cannot be more relevant here than this repo's own record.
// Every drop is recorded; never a silent cut.
//
// Contracts are the one exception to origin-outranks-kind, and deliberately so
// (ADR-0033): a contract naming this repo is an OBLIGATION on this code, not
// advice about it, so a peer-declared contract still outranks a local decision.
// The set is bounded by "contracts that named us", it never displaces the spec,
// and local still sorts before peer within the section.
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
				b.Dropped = append(b.Dropped, kind+" "+it.QualifiedID()+" (over budget)")
			}
		}
		return kept
	}
	// Local knowledge fills first, in kind priority; peer knowledge fills what
	// is left, in the same kind priority. Concatenating kept-local ++ kept-peer
	// restores each section's sorted order exactly (sortItems ranks local
	// first), so this changes what SURVIVES the budget, never the order.
	b.Contracts = add(b.Contracts, "contract")
	// Shared systems fill next (ADR-0034): "the thing you are editing belongs to
	// another repo" frames every item below it, and the section is bounded by
	// the landscape globs that matched this one path. It never displaces the
	// spec — the spec is part of the mandatory baseline above — and each drop is
	// recorded like every other cut.
	var land []LandscapeItem
	for _, li := range b.Landscape {
		if used+li.tokens <= budget {
			used += li.tokens
			land = append(land, li)
		} else {
			b.Truncated = true
			b.Dropped = append(b.Dropped, "landscape "+li.ID+" (over budget)")
		}
	}
	b.Landscape = land
	localD, peerD := splitOrigin(b.Decisions)
	localL, peerL := splitOrigin(b.Lessons)
	localR, peerR := splitOrigin(b.References)
	localD = add(localD, "decision")
	localL = add(localL, "lesson")
	localR = add(localR, "reference")
	b.Decisions = append(localD, add(peerD, "decision")...)
	b.Lessons = append(localL, add(peerL, "lesson")...)
	b.References = append(localR, add(peerR, "reference")...)
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
