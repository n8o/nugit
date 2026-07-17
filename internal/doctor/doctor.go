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

	// Informational, never a pre-flight failure (OK is always true): proposed
	// objects are a healthy candidate lane (ADR-0016), just one awaiting review.
	r.Checks = append(r.Checks, Check{Name: "proposed objects pending ratification",
		OK: true, Advisory: true, Detail: pendingDetail(objs)})

	wired, wdetail := mcpWired(repoDir)
	r.Checks = append(r.Checks, Check{Name: "MCP wired", OK: wired, Advisory: true, Detail: wdetail})

	if kerr == nil {
		h := storeHealth(m, objs, len(bad))
		r.Health = &h
	}

	return r
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
func storeHealth(m model.Model, objs []model.KnowledgeObject, untyped int) StoreHealth {
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
		deduct(p, fmt.Sprintf("%d file(s) invisible to retrieval (untyped front-matter)", untyped))
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
// live (not superseded/invalidated) and awaiting `nugit ratify`.
func pendingDetail(objs []model.KnowledgeObject) string {
	var ids []string
	for _, o := range objs {
		if o.ID == "" || o.Status != model.StatusProposed {
			continue
		}
		if o.EffectiveStatus == model.StatusSuperseded || o.EffectiveStatus == model.StatusInvalidated {
			continue
		}
		ids = append(ids, o.ID)
	}
	if len(ids) == 0 {
		return "none"
	}
	sort.Strings(ids)
	shown := ids
	if len(shown) > 5 {
		shown = shown[:5]
	}
	s := fmt.Sprintf("%d pending: %s", len(ids), strings.Join(shown, ", "))
	if len(ids) > len(shown) {
		s += fmt.Sprintf(" (+%d more)", len(ids)-len(shown))
	}
	return s + " — run 'nugit ratify -list'"
}

// untypedObjects finds knowledge files that would silently vanish from
// retrieval: a front-matter block that fails to parse (e.g. `supersedes:` as a
// YAML list — the schema is a single string) or parses without id/type. Found
// in the wild on a pilot repo, where a list-form supersedes made an ADR invisible to
// every context() bundle.
func untypedObjects(repoDir string) []string {
	var bad []string
	check := func(rel string) {
		b, err := os.ReadFile(filepath.Join(repoDir, rel))
		if err != nil {
			return
		}
		obj, ok := knowledge.ParseObject(rel, string(b))
		switch {
		case !ok:
			bad = append(bad, rel+" (no front-matter block)")
		case obj.ID == "" || obj.Type == "":
			bad = append(bad, rel+" (front-matter fails the schema — e.g. supersedes must be a single string, not a list)")
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

func untypedDetail(bad []string) string {
	if len(bad) == 0 {
		return "every object carries valid typed front-matter"
	}
	shown := bad
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
