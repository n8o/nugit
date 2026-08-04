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
	"github.com/n8o/nugit/internal/model"
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

	hooks := gitutil.Repo{Dir: repoDir}.HooksDir()
	hookInstalled := false
	if hooks != "" {
		_, e := os.Stat(filepath.Join(hooks, "commit-msg"))
		hookInstalled = e == nil
	}
	add("commit-msg hook installed", hookInstalled, hookDetail(hookInstalled))

	add("language backend detected", backend(repoDir) != "", backend(repoDir))

	objs, kerr := knowledge.Load(repoDir)
	add("knowledge store loads", kerr == nil, fmt.Sprintf("%d object(s)", len(objs)))

	bad := untypedObjects(repoDir)
	add("knowledge objects are typed", len(bad) == 0, untypedDetail(bad))

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

	if kerr == nil {
		h := storeHealth(m, objs, bad)
		r.Health = &h
	}

	return r
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
var provenanceKnownKeys = map[string]bool{"commit": true, "agent": true, "citation": true}

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
			issues = append(issues, fmt.Sprintf("%s: unknown provenance key(s) %s — the schema drops them silently (known: commit, agent, citation)",
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

func hookDetail(ok bool) string {
	if ok {
		return "validates trailer blocks on commit"
	}
	return "run `nugit init` to install"
}
