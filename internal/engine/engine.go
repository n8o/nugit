// Package engine orchestrates the keystone pipeline: two git refs in, a fully
// computed model.Report out. This is the single seam the CLI and tests drive.
package engine

import (
	"github.com/burrowfarm/nugit/internal/consistency"
	"github.com/burrowfarm/nugit/internal/delta"
	"github.com/burrowfarm/nugit/internal/gitutil"
	"github.com/burrowfarm/nugit/internal/goimports"
	"github.com/burrowfarm/nugit/internal/knowledge"
	"github.com/burrowfarm/nugit/internal/mapping"
	"github.com/burrowfarm/nugit/internal/model"
	"github.com/burrowfarm/nugit/internal/significance"
	"github.com/burrowfarm/nugit/internal/trailers"
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
	if opt.DSLPath == "" {
		opt.DSLPath = delta.DefaultDSLPath
	}
	repo := gitutil.Repo{Dir: opt.RepoDir}
	base := repo.MergeBase(opt.Base, opt.Head)

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
	module, _ := goimports.ModulePath(opt.RepoDir)

	in := consistency.Input{
		Repo:       repo,
		RepoDir:    opt.RepoDir,
		Module:     module,
		HeadModel:  headModel,
		Mapper:     mp,
		Code:       codeDelta,
		C4:         c4Delta,
		Knowledge:  knowDelta,
		AllObjects: allObjs,
		Commits:    commits,
	}

	// Order matters: C4<->code first (independent), then significance (uses it),
	// then the checks that depend on the architectural verdict.
	c4Findings := consistency.C4CodeFindings(in)
	sig := significance.Classify(c4Delta, codeDelta, knowDelta, len(c4Findings) > 0)
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
