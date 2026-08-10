// Package doctor is a fast pre-flight that verifies a nugit adoption is healthy:
// config parses, the C4 model parses and is non-empty, the commit-msg hook is
// installed, a language backend is detected, and the knowledge store loads. It
// answers "is nugit set up correctly here?" without running a full pr-render.
package doctor

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/bootstrap"
	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/modelfacts"
)

// Check is one health probe.
type Check struct {
	Name string
	OK   bool
	// Advisory checks inform without gating: they never affect AllOK or the
	// doctor exit code (e.g. "MCP wired" — useful, not mandatory).
	Advisory bool
	Detail   string
}

// StoreHealth is a descriptive (never gating) snapshot of the knowledge store.
type StoreHealth struct {
	ByType   map[string]int // decision / lesson / spec / reference / glossary
	ByStatus map[string]int // effective status counts
	Untyped  int            // files invisible to retrieval (silent-untype)
	// OrphanComponents have zero scoped knowledge (scope match only; edges
	// deliberately not counted — this measures where capture is thin).
	OrphanComponents []string
	ProposedPending  int // candidate lane awaiting `nugit ratify` (ADR-0016)
	Score            int // 0..100, descriptive only — see Reasons
	Reasons          []string
}

// Report is the full pre-flight result.
type Report struct {
	Checks []Check
	// Health is nil when the store failed to load.
	Health *StoreHealth
}

// AllOK reports whether every gating (non-advisory) check passed.
func (r Report) AllOK() bool {
	for _, c := range r.Checks {
		if !c.OK && !c.Advisory {
			return false
		}
	}
	return true
}

// Run probes the nugit adoption rooted at repoDir.
func Run(repoDir string) Report {
	var r Report
	add := func(name string, ok bool, detail string) {
		r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: detail})
	}

	cfg, err := config.Load(repoDir)
	add("config.yml parses", err == nil, errOr(err, "c4.mode="+cfg.C4.Mode))

	dslPath := cfg.C4.DSL
	if dslPath == "" {
		dslPath = ".nugit/architecture/workspace.dsl"
	}
	src, derr := os.ReadFile(filepath.Join(repoDir, dslPath))
	m := c4.Parse(string(src))
	add("workspace.dsl parses with components", derr == nil && len(m.Components) > 0,
		fmt.Sprintf("%d component(s), %d relationship(s)", len(m.Components), len(m.Relationships)))

	hook := gitutil.Repo{Dir: repoDir}.CommitMsgHook()
	add("commit-msg hook installed", hook.Installed(), hookDetail(repoDir, hook))

	add("language backend detected", backend(repoDir) != "", backend(repoDir))

	objs, kerr := knowledge.Load(repoDir)
	add("knowledge store loads", kerr == nil, fmt.Sprintf("%d object(s)", len(objs)))

	bad := untypedObjects(repoDir)
	add("knowledge objects are typed", len(bad) == 0, untypedDetail(bad))

	// GATING, like the typed check above and for the same reason: both are silent
	// data loss. An untyped object vanishes from retrieval; a duplicated id makes
	// one object shadow another everywhere identity resolves by id (ADR-0039).
	// Unlike the ADR-0022 lifecycle checks this is an exact grouping over
	// committed text, so it has no false positives to be advisory about.
	dups := knowledge.DuplicateIDs(objs)
	add("knowledge object ids are unique", len(dups) == 0, duplicateIDDetail(dups))

	// Lifecycle integrity (ADR-0022), both advisory: a supersession declared
	// only in prose leaves the target serving as live, and a drifting
	// provenance block silently loses data.
	prose := knowledge.ProseOnlySupersessions(objs)
	r.Checks = append(r.Checks, Check{Name: "supersession edges match prose",
		OK: len(prose) == 0, Advisory: true, Detail: proseDetail(prose)})

	prov := provenanceIssues(repoDir, objs)
	r.Checks = append(r.Checks, Check{Name: "provenance is sane",
		OK: len(prov) == 0, Advisory: true, Detail: provDetail(prov)})

	// Informational, never a pre-flight failure (OK is always true): proposed
	// objects are a healthy candidate lane (ADR-0016), just one awaiting review.
	r.Checks = append(r.Checks, Check{Name: "proposed objects pending ratification",
		OK: true, Advisory: true, Detail: pendingDetail(objs, time.Now())})

	wired, wdetail := mcpWired(repoDir)
	r.Checks = append(r.Checks, Check{Name: "MCP wired", OK: wired, Advisory: true, Detail: wdetail})

	// Peer stores (ADR-0032). Always advisory: an unreachable peer is the normal
	// CI state (only the repo under review is checked out) and must never gate.
	pok, pdetail := peerStores(repoDir, cfg)
	r.Checks = append(r.Checks, Check{Name: "peer stores reachable",
		OK: pok, Advisory: true, Detail: pdetail})

	// Org hub (ADR-0035). Advisory: a hub is a peer with a role, so every way it
	// can be absent degrades exactly like an absent peer — and each of those ways
	// gets its own sentence, because "typo in org.hub" and "sibling not checked
	// out in CI" have nothing to do with each other.
	hok, hdetail := orgHub(repoDir, cfg)
	r.Checks = append(r.Checks, Check{Name: "org hub", OK: hok, Advisory: true, Detail: hdetail})

	// Cross-repo contracts (ADR-0033). Advisory, always: enforcing an obligation
	// is `pr-render`'s job at the reviewed ref, and doctor is a pre-flight — an
	// unmet obligation is a backlog item, never a reason to block setup.
	cok, cdetail := contractObligations(repoDir, cfg, objs)
	r.Checks = append(r.Checks, Check{Name: "cross-repo contract obligations",
		OK: cok, Advisory: true, Detail: cdetail})

	// Org landscape (ADR-0034). Advisory: the landscape is optional, an absent
	// peer is the normal CI state, and a dangling owner id is modelling debt.
	lok, ldetail := landscapeHealth(repoDir, cfg)
	r.Checks = append(r.Checks, Check{Name: "org landscape",
		OK: lok, Advisory: true, Detail: ldetail})

	// ADR-0026: wiring coherence between config.yml and the artifacts that
	// invoke nugit (CI workflows, CLAUDE.md, skill files). Advisory only.
	r.Checks = append(r.Checks, wiringChecks(repoDir, cfg)...)
	// Full-repo facts-vs-DSL diff (ADR-0021): the periodic drift scan the
	// PR-scoped model-drift check deliberately doesn't do. Advisory: modeling
	// debt is a backlog to work, never a pre-flight failure.
	if len(m.Components) > 0 && !m.Structural() {
		covOK, covDetail := modelCoverage(repoDir, m)
		r.Checks = append(r.Checks, Check{Name: "model covers detected units",
			OK: covOK, Advisory: true, Detail: covDetail})
	}

	if kerr == nil {
		h := storeHealth(m, objs, bad)
		r.Health = &h
	}

	return r
}

// duplicateIDDetail words the duplicate-id check (ADR-0039). Every colliding id
// and every file carrying it is named — deliberately untruncated, unlike the
// advisory details above: this check gates, and its remediation is "open these
// exact files", which a "… 3 more" tail would send the reader searching for.
func duplicateIDDetail(dups []knowledge.DuplicateID) string {
	if len(dups) == 0 {
		return "every id is carried by exactly one file"
	}
	items := make([]string, 0, len(dups))
	for _, d := range dups {
		items = append(items, fmt.Sprintf("%s is carried by %d files (%s)",
			d.QualifiedID(), len(d.Paths), strings.Join(d.Paths, ", ")))
	}
	return fmt.Sprintf("%d duplicated id(s): %s — one object shadows the other in retrieval, in `nugit ratify`, and in every `relates_to`/`supersedes`/`amends` edge naming it; give each record its own id",
		len(dups), strings.Join(items, "; "))
}

// proseDetail words the prose-only supersession check (ADR-0022).
func proseDetail(ps []knowledge.ProseSupersession) string {
	if len(ps) == 0 {
		return "no supersession is declared in prose only"
	}
	var items []string
	for _, p := range ps {
		items = append(items, fmt.Sprintf("%s says it supersedes %s but declares no edge — add `supersedes: %s` (or `amends:%s`) so EffectiveStatus updates",
			p.ObjectID, p.Target, p.Target, p.Target))
	}
	shown := items
	if len(shown) > 3 {
		shown = append(append([]string{}, shown[:3]...), fmt.Sprintf("… %d more", len(items)-3))
	}
	return fmt.Sprintf("%d supersession(s) declared in prose only: %s", len(ps), strings.Join(shown, "; "))
}

// provenanceKnownKeys are the schema fields of a provenance block; anything
// else is silently dropped by the typed parser (seen in the wild: an `issues:`
// array that vanished without a sound).
var provenanceKnownKeys = map[string]bool{
	"commit": true, "agent": true, "citation": true,
	// Stamped by `nugit promote` on a record copied into the org hub (ADR-0035).
	"origin_repo": true, "origin_path": true,
}

// provenanceIssues audits provenance blocks syntactically (ADR-0022): a
// literal HEAD or empty commit is meaningless as provenance, and unknown keys
// are data the schema drops. Deliberately NOT verified: sha resolvability
// (squash-merge legitimately orphans feature-branch shas — ADR-0005) and slug
// values like `bootstrap` (the historic bootstrap idiom).
func provenanceIssues(repoDir string, objs []model.KnowledgeObject) []string {
	var issues []string
	for _, o := range objs {
		if o.ID == "" || o.Type == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repoDir, o.Path))
		if err != nil {
			continue
		}
		raw, ok := knowledge.RawFrontMatter(string(b))
		if !ok {
			continue
		}
		pv, present := raw["provenance"]
		if !present {
			continue // absent is fine; doctor never demands provenance
		}
		pm, isMap := pv.(map[string]any)
		if !isMap {
			issues = append(issues, o.Path+": provenance is not a mapping")
			continue
		}
		if cv, has := pm["commit"]; has {
			s, _ := cv.(string)
			switch {
			case strings.EqualFold(s, "HEAD"):
				issues = append(issues, o.Path+": provenance.commit is literal HEAD (a moving ref) — record the actual sha, or `seed`")
			case strings.TrimSpace(s) == "":
				issues = append(issues, o.Path+": provenance.commit is empty — record the sha, use `seed`, or drop the key")
			}
		}
		var unknown []string
		for k := range pm {
			if !provenanceKnownKeys[k] {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			issues = append(issues, fmt.Sprintf("%s: unknown provenance key(s) %s — the schema drops them silently (known: commit, agent, citation, origin_repo, origin_path)",
				o.Path, strings.Join(unknown, ", ")))
		}
	}
	sort.Strings(issues)
	return issues
}

func provDetail(issues []string) string {
	if len(issues) == 0 {
		return "every provenance block is well-formed"
	}
	shown := issues
	if len(shown) > 3 {
		shown = append(append([]string{}, shown[:3]...), fmt.Sprintf("… %d more", len(issues)-3))
	}
	return fmt.Sprintf("%d provenance issue(s): %s", len(issues), strings.Join(shown, "; "))
}

// modelCoverage diffs the full detected-unit inventory against the model
// (ADR-0021's doctor surface). Same core as the PR-scoped model-drift check,
// without the touched-dirs filter.
func modelCoverage(repoDir string, m model.Model) (bool, string) {
	var names []string
	for _, c := range m.Components {
		names = append(names, c.ID, c.Name)
	}
	for _, ct := range m.Containers {
		names = append(names, ct.ID, ct.Name)
	}
	prefix := gitutil.Repo{Dir: repoDir}.Prefix()
	unmodeled := modelfacts.Unmodeled(modelfacts.Units(repoDir),
		prefix, mapping.New(m).ResolveDir, names)
	if len(unmodeled) == 0 {
		return true, "every detected buildable/deployable unit maps to a model element"
	}
	dirs := make([]string, 0, len(unmodeled))
	for _, u := range unmodeled {
		dirs = append(dirs, u.Dir)
	}
	shown := dirs
	if len(shown) > 5 {
		shown = shown[:5]
	}
	s := fmt.Sprintf("%d detected unit(s) missing from workspace.dsl: %s", len(dirs), strings.Join(shown, ", "))
	if len(dirs) > len(shown) {
		s += fmt.Sprintf(" (+%d more)", len(dirs)-len(shown))
	}
	return false, s + " — run the nugit-model skill or add stubs"
}

// peerStores reports what each configured peer contributed (ADR-0032): whether
// its checkout is readable here and how many objects it carries. Advisory by
// construction — federation is additive context, so a missing sibling is a
// note, never a failure. A repo with no `peers:` block says exactly that.
func peerStores(repoDir string, cfg config.Config) (bool, string) {
	if len(cfg.Peers) == 0 {
		return true, "no peers configured"
	}
	ok := true
	items := make([]string, 0, len(cfg.Peers))
	for _, p := range cfg.Peers {
		objs, load := knowledge.LoadPeer(knowledge.PeerSource{Name: p.Name, Dir: p.Dir(repoDir)})
		if !load.Reachable {
			ok = false
			items = append(items, fmt.Sprintf("%s: unreachable (%s) — contributes nothing", p.Name, load.Note))
			continue
		}
		items = append(items, fmt.Sprintf("%s: %d object(s), %d global+ratified", p.Name, len(objs), federatable(objs)))
	}
	sort.Strings(items)
	return ok, strings.Join(items, "; ")
}

// federatable counts the peer objects retrieval would actually consider: global
// scope, ratified status. It is the honest number — total object count overstates
// reach, because a peer's component-scoped knowledge names nothing here.
func federatable(objs []model.KnowledgeObject) int {
	n := 0
	for _, o := range objs {
		if o.ID == "" || (o.Scope != "" && o.Scope != "global") {
			continue
		}
		st := o.EffectiveStatus
		if st == "" {
			st = o.Status
		}
		if st == model.StatusAccepted || st == model.StatusActive {
			n++
		}
	}
	return n
}

// contractObligations reports how many ratified contracts — local or from a
// peer — name THIS repo as a party, and how many of their obligations are
// currently unmet (ADR-0033). Advisory by construction, and inert without an
// `org.repo` identity: nugit never guesses which party a repo is, so "not
// configured" is a distinct, stated outcome rather than a silent zero.
//
// Doctor reads the WORKING TREE, unlike the PR-time check which reads the
// reviewed ref: doctor's question is "how does this checkout stand right now",
// and its answer gates nothing.
func contractObligations(repoDir string, cfg config.Config, local []model.KnowledgeObject) (bool, string) {
	if cfg.Org.Repo == "" {
		return true, "org.repo is not configured — contract checking is inert (nugit never guesses which party this repo is)"
	}
	if cfg.Contracts.Mode == "off" {
		return true, "contracts.mode: off — obligations are not checked (org.repo=" + cfg.Org.Repo + ")"
	}
	objs := append([]model.KnowledgeObject{}, local...)
	srcs := make([]knowledge.PeerSource, 0, len(cfg.Peers))
	for _, p := range cfg.Peers {
		srcs = append(srcs, knowledge.PeerSource{Name: p.Name, Dir: p.Dir(repoDir), Hub: p.Hub})
	}
	objs = append(objs, knowledge.PeerContracts(srcs)...)

	naming := knowledge.ContractsNaming(objs, cfg.Org.Repo)
	if len(naming) == 0 {
		return true, fmt.Sprintf("no ratified contract names %q as a party", cfg.Org.Repo)
	}
	obs := knowledge.Obligations(objs, cfg.Org.Repo, treeReader(repoDir))
	unmet := knowledge.UnmetObligations(obs)
	s := fmt.Sprintf("%d contract(s) name %q, %d obligation(s), %d unmet",
		len(naming), cfg.Org.Repo, len(obs), len(unmet))
	if len(unmet) == 0 {
		return true, s
	}
	items := make([]string, 0, len(unmet))
	for _, ob := range unmet {
		items = append(items, fmt.Sprintf("%s (%s): %s", ob.QualifiedID(), ob.OriginLabel(), ob.Must.Name))
	}
	sort.Strings(items)
	return false, s + " — " + strings.Join(items, "; ")
}

// treeReader reads asserted files from the working tree, for doctor only.
func treeReader(repoDir string) knowledge.FileReader {
	return func(rel string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(repoDir, filepath.FromSlash(strings.TrimPrefix(rel, "./"))))
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

// mcpJSON is the subset of .mcp.json doctor inspects.
type mcpJSON struct {
	Servers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcpServers"`
}

// mcpWired reports whether .mcp.json exposes the context() MCP tool — i.e.
// some server entry runs `nugit ... mcp`. Advisory: retrieval works without
// it, but agents can't call context() until it's wired.
func mcpWired(repoDir string) (bool, string) {
	b, err := os.ReadFile(filepath.Join(repoDir, ".mcp.json"))
	if err != nil {
		return false, "no .mcp.json — run `nugit agent -client claude-code -install`" + lookPathHint()
	}
	var cfg mcpJSON
	if json.Unmarshal(b, &cfg) != nil {
		return false, ".mcp.json is not valid JSON"
	}
	var matches []string
	for name, s := range cfg.Servers {
		hasMCP := false
		for _, a := range s.Args {
			if a == "mcp" {
				hasMCP = true
			}
		}
		if strings.Contains(s.Command, "nugit") && hasMCP {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return false, ".mcp.json has no nugit mcp server — run `nugit agent -client claude-code -install`"
	}
	sort.Strings(matches)
	return true, fmt.Sprintf("server %q runs nugit mcp", matches[0]) + lookPathHint()
}

func lookPathHint() string {
	if _, err := exec.LookPath("nugit"); err != nil {
		return " (nugit is not on PATH — `go install github.com/n8o/nugit/cmd/nugit@latest`)"
	}
	return ""
}

// storeHealth summarizes the knowledge store descriptively. The score is a
// direction indicator with reasons, never a gate — doctor's exit code ignores
// it by design (a number to move, not a number to fail on).
func storeHealth(m model.Model, objs []model.KnowledgeObject, bad []badFile) StoreHealth {
	untyped := len(bad)
	h := StoreHealth{ByType: map[string]int{}, ByStatus: map[string]int{}, Untyped: untyped}
	scoped := map[string]bool{}
	for _, o := range objs {
		if o.ID == "" || o.Type == "" {
			continue
		}
		h.ByType[string(o.Type)]++
		st := o.EffectiveStatus
		if st == "" {
			st = o.Status
		}
		if st != "" {
			h.ByStatus[string(st)]++
		}
		if o.Status == model.StatusProposed &&
			o.EffectiveStatus != model.StatusSuperseded && o.EffectiveStatus != model.StatusInvalidated {
			h.ProposedPending++
		}
		if o.Scope != "" && o.Scope != "global" {
			scoped[o.Scope] = true
		}
	}
	for _, c := range m.Components {
		if !scoped[c.ID] {
			h.OrphanComponents = append(h.OrphanComponents, c.ID)
		}
	}
	sort.Strings(h.OrphanComponents)

	score := 100
	deduct := func(points int, reason string) {
		score -= points
		h.Reasons = append(h.Reasons, reason)
	}
	if untyped > 0 {
		p := 15 * untyped
		if p > 30 {
			p = 30
		}
		// A targeted reason when the untype cause is known (ADR-0022): name the
		// list-authored scalar field instead of the generic message.
		fieldSet := map[string]bool{}
		listCount := 0
		for _, bf := range bad {
			if len(bf.ListFields) > 0 {
				listCount++
				for _, f := range bf.ListFields {
					fieldSet[f] = true
				}
			}
		}
		reason := fmt.Sprintf("%d file(s) invisible to retrieval (untyped front-matter)", untyped)
		if listCount > 0 {
			fields := make([]string, 0, len(fieldSet))
			for f := range fieldSet {
				fields = append(fields, "`"+f+":`")
			}
			sort.Strings(fields)
			reason = fmt.Sprintf("%d file(s) invisible to retrieval (untyped front-matter; %d untyped by a list-valued %s — the schema takes a single string)",
				untyped, listCount, strings.Join(fields, ", "))
		}
		deduct(p, reason)
	}
	if n, total := len(h.OrphanComponents), len(m.Components); n > 0 && total > 0 {
		deduct(int(math.Round(40*float64(n)/float64(total))),
			fmt.Sprintf("%d/%d component(s) have no scoped knowledge", n, total))
	}
	if h.ByType["decision"]+h.ByType["lesson"] == 0 && len(m.Components) > 0 {
		deduct(20, "no captured decisions or lessons yet")
	}
	if h.ProposedPending > 0 {
		p := 5 * h.ProposedPending
		if p > 15 {
			p = 15
		}
		deduct(p, fmt.Sprintf("%d distilled object(s) awaiting review (`nugit ratify -list`)", h.ProposedPending))
	}
	if score < 0 {
		score = 0
	}
	h.Score = score
	return h
}

// CountsLine renders the store composition on one line, fixed order.
func (h StoreHealth) CountsLine() string {
	left := fmt.Sprintf("decisions: %d  lessons: %d  specs: %d  references: %d",
		h.ByType["decision"], h.ByType["lesson"], h.ByType["spec"], h.ByType["reference"])
	right := fmt.Sprintf("proposed: %d  superseded: %d", h.ProposedPending, h.ByStatus["superseded"])
	return left + " | " + right
}

// pendingDetail summarizes the candidate lane: proposed objects that are still
// live (not superseded/invalidated) and awaiting `nugit ratify`. Each entry
// carries its age in days from `created:` (ADR-0022) — a proposed object weeks
// old is drift, not churn — but the line stays informational (ADR-0016).
func pendingDetail(objs []model.KnowledgeObject, now time.Time) string {
	byID := map[string]model.KnowledgeObject{}
	var ids []string
	for _, o := range objs {
		if o.ID == "" || o.Status != model.StatusProposed {
			continue
		}
		if o.EffectiveStatus == model.StatusSuperseded || o.EffectiveStatus == model.StatusInvalidated {
			continue
		}
		ids = append(ids, o.ID)
		byID[o.ID] = o
	}
	if len(ids) == 0 {
		return "none"
	}
	sort.Strings(ids)
	shown := ids
	if len(shown) > 5 {
		shown = shown[:5]
	}
	labels := make([]string, 0, len(shown))
	for _, id := range shown {
		labels = append(labels, pendingLabel(byID[id], now))
	}
	s := fmt.Sprintf("%d pending: %s", len(ids), strings.Join(labels, ", "))
	if len(ids) > len(shown) {
		s += fmt.Sprintf(" (+%d more)", len(ids)-len(shown))
	}
	return s + " — run 'nugit ratify -list'"
}

// pendingLabel renders one pending entry, with its age when `created:` is set.
func pendingLabel(o model.KnowledgeObject, now time.Time) string {
	if o.Created.IsZero() {
		return o.ID
	}
	days := int(now.Sub(o.Created).Hours() / 24)
	if days < 0 {
		days = 0
	}
	return fmt.Sprintf("%s (%dd)", o.ID, days)
}

// badFile is one knowledge file invisible to retrieval, with the sharpest
// diagnosis doctor can make of WHY.
type badFile struct {
	Rel    string
	Detail string
	// ListFields are schema scalar fields authored as YAML lists — the exact
	// mistake that silently untypes an object (the pilot bug ADR-0022 names:
	// `supersedes:` as a list downgraded an ADR out of every context() bundle).
	ListFields []string
}

// scalarStringFields are front-matter fields the schema types as single
// strings; authored as YAML lists they fail the typed parse and untype the
// whole object.
var scalarStringFields = []string{"id", "type", "scope", "status", "supersedes", "confidence", "source"}

// untypedObjects finds knowledge files that would silently vanish from
// retrieval: a front-matter block that fails to parse or parses without
// id/type. Found in the wild on a pilot repo, where a list-form supersedes
// made an ADR invisible to every context() bundle — that case is diagnosed
// specifically, naming the field (ADR-0022).
func untypedObjects(repoDir string) []badFile {
	var bad []badFile
	check := func(rel string) {
		b, err := os.ReadFile(filepath.Join(repoDir, rel))
		if err != nil {
			return
		}
		obj, ok := knowledge.ParseObject(rel, string(b))
		switch {
		case !ok:
			bad = append(bad, badFile{Rel: rel, Detail: "no front-matter block"})
		case obj.ID == "" || obj.Type == "":
			if fields := scalarListFields(string(b)); len(fields) > 0 {
				quoted := make([]string, len(fields))
				for i, f := range fields {
					quoted[i] = "`" + f + ":`"
				}
				bad = append(bad, badFile{Rel: rel, ListFields: fields,
					Detail: strings.Join(quoted, ", ") + " must be a single string, not a YAML list — the list silently untypes the whole object; one supersession per record (split it, or use `relates_to: [amends:<id>]`)"})
				return
			}
			bad = append(bad, badFile{Rel: rel, Detail: "front-matter fails the schema — e.g. supersedes must be a single string, not a list"})
		}
	}
	for _, d := range []string{"decisions", "lessons", "specs", "references"} {
		entries, err := os.ReadDir(filepath.Join(repoDir, ".nugit", d))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
				check(filepath.Join(".nugit", d, e.Name()))
			}
		}
	}
	check(filepath.Join(".nugit", "glossary.md"))
	return bad
}

// scalarListFields reports which string-typed schema fields the raw
// front-matter authored as YAML lists.
func scalarListFields(content string) []string {
	raw, ok := knowledge.RawFrontMatter(content)
	if !ok {
		return nil
	}
	var fields []string
	for _, f := range scalarStringFields {
		if _, isList := raw[f].([]any); isList {
			fields = append(fields, f)
		}
	}
	return fields
}

func untypedDetail(bad []badFile) string {
	if len(bad) == 0 {
		return "every object carries valid typed front-matter"
	}
	items := make([]string, 0, len(bad))
	for _, bf := range bad {
		items = append(items, bf.Rel+" ("+bf.Detail+")")
	}
	shown := items
	if len(shown) > 3 {
		shown = append(append([]string{}, shown[:3]...), fmt.Sprintf("… %d more", len(bad)-3))
	}
	return fmt.Sprintf("%d file(s) invisible to retrieval: %s", len(bad), strings.Join(shown, "; "))
}

// backend names the analyzer nugit init would use here (matches the auto-detect order).
func backend(repoDir string) string {
	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err == nil {
		return "Go (import graph)"
	}
	switch {
	case bootstrap.DetectCMake(repoDir):
		return "C++ (CMake)"
	case bootstrap.DetectPython(repoDir):
		return "Python (imports)"
	case bootstrap.DetectTS(repoDir):
		return "TypeScript (dependency-cruiser)"
	default:
		return "structural (directory layout)"
	}
}

func errOr(err error, ok string) string {
	if err != nil {
		return err.Error()
	}
	return ok
}

// hookDetail words the commit-msg hook check. It always names WHERE the hook
// was found (or where one belongs), because under a shim-generating hook
// manager that is not `.git/hooks` and a reader cannot guess which convention
// the repo is running.
func hookDetail(repoDir string, h gitutil.CommitMsgHook) string {
	rel := relTo(repoDir)
	if h.Installed() {
		s := "validates trailer blocks on commit — found at " + rel(h.Path)
		if h.Shimmed() {
			s += fmt.Sprintf(" (core.hooksPath=%s is a generated shim dir; its shims run this file)", rel(h.GitHooksDir))
		}
		return s
	}
	if h.Target == "" {
		return "not a git repo (or git is unavailable) — nothing to install into"
	}
	s := "run `nugit init` to install it at " + rel(h.Target)
	if f := h.ForeignAtTarget(); f != "" {
		s += fmt.Sprintf("; %s already holds a hook nugit did not write, and nugit never overwrites one — chain it by hand instead (`nugit hook commit-msg \"$1\"`)", rel(f))
	}
	if h.Inert != "" {
		s += fmt.Sprintf("; a nugit hook sits at %s but core.hooksPath sends git to %s, so it never runs", rel(h.Inert), rel(h.GitHooksDir))
	}
	return s
}

// relTo renders absolute paths relative to repoDir when they are inside it,
// and verbatim otherwise (a linked worktree's hooks live in the main checkout).
func relTo(repoDir string) func(string) string {
	base, err := filepath.Abs(repoDir)
	return func(p string) string {
		if err != nil || p == "" {
			return p
		}
		r, rerr := filepath.Rel(base, p)
		if rerr != nil || strings.HasPrefix(r, "..") {
			return p
		}
		return filepath.ToSlash(r)
	}
}
