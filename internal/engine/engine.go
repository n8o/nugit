// Package engine orchestrates the keystone pipeline: two git refs in, a fully
// computed model.Report out. This is the single seam the CLI and tests drive.
package engine

import (
	"fmt"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/consistency"
	"github.com/n8o/nugit/internal/delta"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/goimports"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/significance"
	"github.com/n8o/nugit/internal/trailers"
)

// Options configure a render run.
type Options struct {
	RepoDir string
	Base    string
	Head    string
	DSLPath string // defaults to delta.DefaultDSLPath
}

// BuildReport computes the four deltas, the significance verdict, and the
// cross-artifact findings for the range (mergeBase(base,head), head].
func BuildReport(opt Options) (model.Report, error) {
	repo := gitutil.Repo{Dir: opt.RepoDir}
	base := repo.MergeBase(opt.Base, opt.Head)

	// Read config at the reviewed ref (like the DSL and import graph), so the
	// verdict never depends on uncommitted working-tree state.
	cfgSrc, _ := repo.ShowFile(opt.Head, ".nugit/config.yml")
	cfg, err := config.LoadBytes([]byte(cfgSrc))
	if err != nil {
		return model.Report{}, fmt.Errorf("parsing .nugit/config.yml at %s: %w", opt.Head, err)
	}
	if opt.DSLPath == "" {
		opt.DSLPath = cfg.C4.DSL
	}
	if opt.DSLPath == "" {
		opt.DSLPath = delta.DefaultDSLPath
	}

	c4Delta, _, headModel, err := delta.C4(repo, base, opt.Head, opt.DSLPath)
	if err != nil {
		return model.Report{}, err
	}
	mp := mapping.New(headModel)

	codeDelta, err := delta.Code(repo, base, opt.Head, mp)
	if err != nil {
		return model.Report{}, err
	}
	knowDelta, err := delta.Knowledge(repo, base, opt.Head)
	if err != nil {
		return model.Report{}, err
	}
	plan := delta.Plan(opt.RepoDir)

	commits, err := repo.Log(base, opt.Head)
	if err != nil {
		return model.Report{}, err
	}
	for i := range commits {
		commits[i].Trailer = trailers.Parse(commits[i].Body)
	}

	allObjs, err := knowledge.Load(opt.RepoDir)
	if err != nil {
		return model.Report{}, err
	}
	// Module path from go.mod at head (fall back to the working tree).
	module := ""
	if src, err := repo.ShowFile(opt.Head, "go.mod"); err == nil && src != "" {
		module = goimports.ParseModulePath(src)
	}
	if module == "" {
		module, _ = goimports.ModulePath(opt.RepoDir)
	}

	in := consistency.Input{
		Repo:       repo,
		RepoDir:    opt.RepoDir,
		Head:       opt.Head,
		Module:     module,
		HeadModel:  headModel,
		Mapper:     mp,
		Code:       codeDelta,
		C4:         c4Delta,
		Knowledge:  knowDelta,
		AllObjects: allObjs,
		Commits:    commits,
		C4Warn:     cfg.C4Warn(),
	}

	// Order matters: C4<->code first (independent), then significance (uses it),
	// then the checks that depend on the architectural verdict.
	c4Findings := consistency.C4CodeFindings(in)
	// The architectural signal is a genuine undeclared cross-component edge ONLY —
	// not model-health warnings, and not undeclared edges while in warn (adoption)
	// mode, so adoption doesn't spuriously trip the decision-coverage nag.
	hasUndeclaredEdge := false
	for _, f := range c4Findings {
		if f.Check == "c4<->code" {
			hasUndeclaredEdge = true
			break
		}
	}
	archSignal := hasUndeclaredEdge && !cfg.C4Warn()
	sig := significance.Classify(c4Delta, codeDelta, knowDelta, archSignal, significance.Options{
		TrivialMaxFiles: cfg.Significance.TrivialMaxFiles,
		TrivialMaxChurn: cfg.Significance.TrivialMaxChurn,
	})
	in.Architectural = sig.Tier == model.TierArchitectural
	other := consistency.OtherFindings(in)
	findings := consistency.Sort(append(c4Findings, other...))

	return model.Report{
		BaseRef:      base,
		HeadRef:      opt.Head,
		Commits:      commits,
		C4:           c4Delta,
		Code:         codeDelta,
		Knowledge:    knowDelta,
		Plan:         plan,
		Findings:     findings,
		Significance: sig,
	}, nil
}
