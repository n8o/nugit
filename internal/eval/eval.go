// Package eval measures whether nugit's heuristics actually WORK, not just that
// they run. It executes the engine over a labeled corpus of realistic scenarios
// and reports significance-classifier accuracy plus consistency-check
// precision/recall. This closes the review's "no eval" gap and acts as a
// regression gate against heuristic drift.
//
// Run it with: go test ./internal/eval -run TestCorpus -v
package eval

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/engine"
	"github.com/n8o/nugit/internal/model"
)

// del marks a path to delete at head.
const del = "\x00DELETE"

// Case is one labeled scenario: a base repo state, a head change, and the
// expected significance tier + which consistency checks should fire.
type Case struct {
	Name       string
	DSL        string            // workspace.dsl at base (defaults to baseDSL)
	DSLHead    string            // optional: model at head (for architectural cases)
	Base       map[string]string // path -> content at base (defaults to baseFiles)
	Head       map[string]string // path -> content changed/added at head (del to remove)
	Config     string            // optional .nugit/config.yml
	HeadMsg    string            // head commit message (for trailer-driven checks)
	WantTier   model.Tier
	WantChecks []string // consistency-check names that SHOULD fire
	WantClean  bool     // true if no fail-severity finding is expected
}

// CaseResult is the scored outcome of one case.
type CaseResult struct {
	Name        string
	GotTier     model.Tier
	TierOK      bool
	GotChecks   []string
	MissChecks  []string // expected but absent (false negative)
	ExtraChecks []string // present but unexpected (false positive)
	CleanOK     bool
	Err         error
}

func (r CaseResult) Pass() bool {
	return r.Err == nil && r.TierOK && r.CleanOK && len(r.MissChecks) == 0 && len(r.ExtraChecks) == 0
}

// Metrics aggregates the corpus run.
type Metrics struct {
	Cases          []CaseResult
	N              int
	TierCorrect    int
	TierAccuracy   float64
	TP, FP, FN     int
	CheckPrecision float64
	CheckRecall    float64
}

func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=e", "GIT_AUTHOR_EMAIL=e@e",
		"GIT_COMMITTER_NAME=e", "GIT_COMMITTER_EMAIL=e@e")
	return cmd.Run()
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func writeF(dir, rel, content string) error {
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func runCase(c Case) CaseResult {
	res := CaseResult{Name: c.Name}
	dir, err := os.MkdirTemp("", "nugiteval-")
	if err != nil {
		res.Err = err
		return res
	}
	defer os.RemoveAll(dir)

	dsl := c.DSL
	if dsl == "" {
		dsl = baseDSL
	}
	base := c.Base
	if base == nil {
		base = baseFiles
	}

	var firstErr error
	must := func(e error) {
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}
	must(gitRun(dir, "init", "-q"))
	must(writeF(dir, "go.mod", "module example.com/m\n\ngo 1.25\n"))
	must(writeF(dir, ".nugit/architecture/workspace.dsl", dsl))
	if c.Config != "" {
		must(writeF(dir, ".nugit/config.yml", c.Config))
	}
	for p, content := range base {
		must(writeF(dir, p, content))
	}
	must(gitRun(dir, "add", "-A"))
	must(gitRun(dir, "commit", "-q", "-m", "base"))
	if firstErr != nil {
		res.Err = firstErr
		return res
	}
	baseSHA, _ := gitOut(dir, "rev-parse", "HEAD")

	if c.DSLHead != "" {
		must(writeF(dir, ".nugit/architecture/workspace.dsl", c.DSLHead))
	}
	for p, content := range c.Head {
		if content == del {
			_ = os.Remove(filepath.Join(dir, p))
		} else {
			must(writeF(dir, p, content))
		}
	}
	must(gitRun(dir, "add", "-A"))
	msg := c.HeadMsg
	if msg == "" {
		msg = "head"
	}
	must(gitRun(dir, "commit", "-q", "-m", msg))
	if firstErr != nil {
		res.Err = firstErr
		return res
	}
	headSHA, _ := gitOut(dir, "rev-parse", "HEAD")

	rep, err := engine.BuildReport(engine.Options{RepoDir: dir, Base: baseSHA, Head: headSHA})
	if err != nil {
		res.Err = err
		return res
	}

	res.GotTier = rep.Significance.Tier
	res.TierOK = rep.Significance.Tier == c.WantTier

	gotSet := map[string]bool{}
	hasFail := false
	for _, f := range rep.Findings {
		gotSet[f.Check] = true
		if f.Severity == model.SevFail {
			hasFail = true
		}
	}
	wantSet := map[string]bool{}
	for _, w := range c.WantChecks {
		wantSet[w] = true
	}
	for g := range gotSet {
		res.GotChecks = append(res.GotChecks, g)
		if !wantSet[g] {
			res.ExtraChecks = append(res.ExtraChecks, g)
		}
	}
	for w := range wantSet {
		if !gotSet[w] {
			res.MissChecks = append(res.MissChecks, w)
		}
	}
	sort.Strings(res.GotChecks)
	sort.Strings(res.ExtraChecks)
	sort.Strings(res.MissChecks)
	res.CleanOK = (!hasFail) == c.WantClean
	return res
}

// Run executes the whole corpus and aggregates metrics.
func Run() Metrics { return RunSet(corpus) }

// RunSet executes any labeled case set and aggregates metrics (used by the
// main corpus and the adversarial spoof set).
func RunSet(cases []Case) Metrics {
	var m Metrics
	for _, c := range cases {
		r := runCase(c)
		m.Cases = append(m.Cases, r)
		m.N++
		if r.TierOK {
			m.TierCorrect++
		}
		want := map[string]bool{}
		for _, w := range c.WantChecks {
			want[w] = true
		}
		got := map[string]bool{}
		for _, g := range r.GotChecks {
			got[g] = true
		}
		for g := range got {
			if want[g] {
				m.TP++
			} else {
				m.FP++
			}
		}
		for w := range want {
			if !got[w] {
				m.FN++
			}
		}
	}
	if m.N > 0 {
		m.TierAccuracy = float64(m.TierCorrect) / float64(m.N)
	}
	if m.TP+m.FP > 0 {
		m.CheckPrecision = float64(m.TP) / float64(m.TP+m.FP)
	}
	if m.TP+m.FN > 0 {
		m.CheckRecall = float64(m.TP) / float64(m.TP+m.FN)
	}
	return m
}

// Report renders a human-readable metrics summary.
func (m Metrics) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "nugit eval — %d cases\n", m.N)
	fmt.Fprintf(&b, "  significance accuracy: %.0f%% (%d/%d)\n", m.TierAccuracy*100, m.TierCorrect, m.N)
	fmt.Fprintf(&b, "  check precision:       %.0f%% (TP=%d FP=%d)\n", m.CheckPrecision*100, m.TP, m.FP)
	fmt.Fprintf(&b, "  check recall:          %.0f%% (TP=%d FN=%d)\n", m.CheckRecall*100, m.TP, m.FN)
	for _, r := range m.Cases {
		status := "ok  "
		if !r.Pass() {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "  [%s] %-28s tier=%s", status, r.Name, r.GotTier)
		if r.Err != nil {
			fmt.Fprintf(&b, " ERR=%v", r.Err)
		}
		if !r.TierOK {
			b.WriteString(" (tier mismatch)")
		}
		if len(r.MissChecks) > 0 {
			fmt.Fprintf(&b, " missing=%v", r.MissChecks)
		}
		if len(r.ExtraChecks) > 0 {
			fmt.Fprintf(&b, " extra=%v", r.ExtraChecks)
		}
		if !r.CleanOK {
			b.WriteString(" (clean mismatch)")
		}
		b.WriteString("\n")
	}
	return b.String()
}
