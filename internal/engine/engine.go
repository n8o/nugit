// Package engine orchestrates the keystone pipeline: two git refs in, a fully
// computed model.Report out. This is the single seam the CLI and tests drive.
package engine

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/consistency"
	"github.com/n8o/nugit/internal/delta"
	"github.com/n8o/nugit/internal/distill"
	"github.com/n8o/nugit/internal/evidence"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/goimports"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/narrative"
	"github.com/n8o/nugit/internal/significance"
	"github.com/n8o/nugit/internal/trailers"
	"github.com/n8o/nugit/internal/wiring"
)

// Options configure a render run.
type Options struct {
	RepoDir string
	Base    string
	Head    string
	DSLPath string // defaults to delta.DefaultDSLPath
	// FailOnFlag is the -fail-on value ONLY when the caller passed it
	// explicitly; "" when the flag defaulted from config. Used to record an
	// enforcement downgrade on the report (ADR-0026) — the engine never owns
	// the exit-code policy itself.
	FailOnFlag string
}

// BuildReport computes the four deltas, the significance verdict, and the
// cross-artifact findings for the range (mergeBase(base,head), head].
func BuildReport(opt Options) (model.Report, error) {
	repo := gitutil.Repo{Dir: opt.RepoDir}
	base := repo.MergeBase(opt.Base, opt.Head)

	// prefix is the nugit root's location within the git repo (e.g.
	// "apps/operator/"), or "" when the nugit root IS the git root. All git paths
	// (ShowFile/diff) are git-root-relative; model globs are written git-root-
	// relative too, so the prefix is the single bridge between the two. When
	// prefix=="" every path below is byte-identical to before — no regression.
	prefix := repo.Prefix()

	// Read config at the reviewed ref (like the DSL and import graph), so the
	// verdict never depends on uncommitted working-tree state.
	cfgSrc, _ := repo.ShowFile(opt.Head, prefix+".nugit/config.yml")
	cfg, err := config.LoadBytes([]byte(cfgSrc))
	if err != nil {
		return model.Report{}, fmt.Errorf("parsing .nugit/config.yml at %s: %w", opt.Head, err)
	}
	if opt.DSLPath == "" {
		dsl := cfg.C4.DSL
		if dsl == "" {
			dsl = delta.DefaultDSLPath
		}
		opt.DSLPath = prefix + dsl // git-root-relative
	}

	c4Delta, _, headModel, err := delta.C4(repo, base, opt.Head, opt.DSLPath)
	if err != nil {
		return model.Report{}, err
	}
	mp := mapping.New(headModel)

	codeDelta, err := delta.Code(repo, base, opt.Head, mp, prefix)
	if err != nil {
		return model.Report{}, err
	}
	knowDelta, err := delta.Knowledge(repo, base, opt.Head, prefix)
	if err != nil {
		return model.Report{}, err
	}
	plan := delta.DiffPlan(repo, base, opt.Head, prefix, delta.PlanScope(cfg.PlanScope()))

	commits, err := repo.Log(base, opt.Head)
	if err != nil {
		return model.Report{}, err
	}
	for i := range commits {
		commits[i].Trailer = trailers.Parse(commits[i].Body)
	}

	// Knowledge is read at the reviewed ref, like the DSL, config, and source —
	// never the working tree (LESSON-read-from-reviewed-ref) — so stale-knowledge
	// and spec-linkage describe base..head even when the checkout has drifted.
	allObjs, err := knowledge.LoadAtRef(repo, opt.Head, prefix)
	if err != nil {
		return model.Report{}, err
	}
	// Module path from go.mod at the nugit root (prefix), at head; fall back to disk.
	module := ""
	if src, err := repo.ShowFile(opt.Head, prefix+"go.mod"); err == nil && src != "" {
		module = goimports.ParseModulePath(src)
	}
	if module == "" {
		module, _ = goimports.ModulePath(opt.RepoDir)
	}

	// Evidence tiers: derived read-time trust labels (never authored). The
	// delta's standalone-parsed objects copy from the resolved head set.
	esig := evidence.Signals{
		Model:   headModel,
		Enforce: !cfg.C4Warn(),
		Backend: module != "" || evidence.BackendActive(opt.RepoDir),
	}
	evidence.Annotate(allObjs, esig)
	evidence.AnnotateDelta(&knowDelta, evidence.Tiers(allObjs), esig)

	in := consistency.Input{
		Repo:       repo,
		RepoDir:    opt.RepoDir,
		Head:       opt.Head,
		Prefix:     prefix,
		Module:     module,
		HeadModel:  headModel,
		Mapper:     mp,
		Code:       codeDelta,
		C4:         c4Delta,
		Knowledge:  knowDelta,
		AllObjects: allObjs,
		Commits:    commits,
		C4Warn:     cfg.C4Warn(),
		Recurrence: consistency.RecurrenceOpts{
			Enabled:    cfg.RecurrenceOn(),
			WindowDays: cfg.Recurrence.WindowDays,
			MinFixes:   cfg.Recurrence.MinFixes,
		},
		Contracts: contractOpts(opt.RepoDir, cfg),
		Landscape: landscapeOpts(opt.RepoDir, cfg),
		TargetRef: opt.Base,
		TargetIDs: targetIDs(repo, opt.Base, base, prefix),
		Wiring:    wiringScan(repo, opt.Head, prefix, cfg),
		WiringCfg: cfg,
	}

	// Order matters: C4<->code first (independent), then significance (uses it),
	// then the checks that depend on the architectural verdict.
	c4Findings := consistency.C4CodeFindings(in)
	// The architectural signal is a genuine undeclared cross-component edge ONLY —
	// not model-health warnings, and not undeclared edges while in warn (adoption)
	// mode, so adoption doesn't spuriously trip the decision-coverage nag.
	hasUndeclaredEdge := false
	for _, f := range c4Findings {
		if consistency.IsUndeclaredEdge(f) {
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
	if cfg.PlanOn() {
		other = append(other, delta.PlanFindings(repo, opt.Head, prefix, plan, codeDelta, cfg.PlanFail())...)
	}
	findings := consistency.Sort(append(c4Findings, other...))

	// PR-time distill (ADR-0018): surface what `nugit distill` would promote
	// for this range. Pure selection over already-loaded inputs — nothing is
	// written at render time, and proposals never become findings.
	proposals, deduped := distill.Propose(commits, allObjs, 1)

	rep := model.Report{
		BaseRef:          base,
		HeadRef:          opt.Head,
		Commits:          commits,
		C4:               c4Delta,
		Code:             codeDelta,
		Knowledge:        knowDelta,
		Plan:             plan,
		Findings:         findings,
		Significance:     sig,
		HeadModel:        headModel,
		Proposals:        proposals,
		ProposalsDeduped: deduped,
	}
	// ADR-0026: an explicit flag weaker than the config-declared policy is
	// honored (visibility, not coercion) but must be visible in every render.
	// Compared against config at head, like every other engine input.
	if opt.FailOnFlag != "" && config.FailOnRank(opt.FailOnFlag) < config.FailOnRank(cfg.PRRender.FailOn) {
		rep.Enforcement = model.NewEnforcementDowngrade(cfg.PRRender.FailOn, opt.FailOnFlag)
	}
	// Opt-in, off by default: inert (no network) unless explicitly enabled +
	// architectural + ANTHROPIC_API_KEY set. Never alters the deterministic facts.
	rep.Narrative = narrative.Generate(rep, opt.RepoDir, cfg.Narrative.Enabled, cfg.Narrative.Model)
	return rep, nil
}

// contractOpts assembles the cross-repo obligation check's input (ADR-0033).
//
// This is the ONE place peer content reaches a pr-render finding, and it is
// gated three ways, all of which this repo controls: `contracts.mode` not off,
// `org.repo` configured (no identity ⇒ inert, never a guess), and a ratified
// contract naming this repo. With any gate shut, no peer store is even read —
// so a repo that has not opted in pays nothing and behaves exactly as it did
// before this decision.
//
// Peer contracts are read from the peer's CHECKOUT, because this repo has no
// ref that addresses another repo's history; every file the obligations assert
// about THIS repo is still read at the reviewed ref (the check owns that). An
// absent peer contributes nothing and can never error (ADR-0032).
func contractOpts(repoDir string, cfg config.Config) consistency.ContractOpts {
	opt := consistency.ContractOpts{
		OrgRepo: cfg.Org.Repo,
		Fail:    cfg.ContractsFail(),
		Off:     !cfg.ContractsOn(),
	}
	if opt.Off || opt.OrgRepo == "" || len(cfg.Peers) == 0 {
		return opt
	}
	srcs := make([]knowledge.PeerSource, 0, len(cfg.Peers))
	for _, p := range cfg.Peers {
		srcs = append(srcs, knowledge.PeerSource{Name: p.Name, Dir: p.Dir(repoDir)})
	}
	opt.PeerContracts = knowledge.PeerContracts(srcs)
	return opt
}

// landscapeOpts assembles the org-landscape ownership check's input (ADR-0034).
//
// Gated exactly like contracts and for the same reason: with no `org.repo` this
// repo cannot know whether it owns a system, so the check is INERT rather than
// guessing. The check itself resolves the landscape — this repo's own copy at
// the reviewed ref, a peer's from its checkout — so a repo that declares no
// identity never reads a peer directory at all.
func landscapeOpts(repoDir string, cfg config.Config) consistency.LandscapeOpts {
	opt := consistency.LandscapeOpts{OrgRepo: cfg.Org.Repo}
	if opt.OrgRepo == "" {
		return opt
	}
	for _, p := range cfg.Peers {
		opt.Peers = append(opt.Peers, knowledge.PeerSource{Name: p.Name, Dir: p.Dir(repoDir), Hub: p.Hub})
	}
	return opt
}

// targetIDs reads the knowledge ids present on the branch this PR merges INTO,
// at that branch's TIP — deliberately not at the merge base the deltas use.
//
// A delta answers "what changed since we diverged", so the merge base is its
// only correct reference. Uniqueness answers "is this id free in the tree I am
// about to join", and the merge base cannot answer that: everything a sibling
// PR merged after we branched is invisible there. That is how one id got minted
// three times from three different bases on the pilot, with every PR green
// (ADR-0041).
//
// Returns nil when the tip IS the merge base (the branch is up to date, so the
// within-store duplicate check already covers everything) or when the ref
// cannot be read — degrading to the pre-ADR-0041 behaviour rather than failing.
func targetIDs(repo gitutil.Repo, targetRef, mergeBase, prefix string) map[string][]string {
	if targetRef == "" {
		return nil
	}
	tip := repo.Resolve(targetRef)
	if tip == "" || tip == mergeBase {
		return nil
	}
	objs, err := knowledge.LoadAtRef(repo, targetRef, prefix)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, o := range objs {
		if o.ID == "" || o.Foreign() {
			continue
		}
		out[o.ID] = append(out[o.ID], o.Path)
	}
	return out
}

// wiringScan takes the ADR-0026 wiring scan at the REVIEWED REF, so the
// PR-time finding is a pure function of (base, head) like every other input —
// doctor's copy reads the checkout, which is right for a pre-flight and wrong
// for a gate (LESSON-read-from-reviewed-ref).
func wiringScan(repo gitutil.Repo, ref, prefix string, cfg config.Config) wiring.Report {
	paths, err := repo.ListTree(ref)
	if err != nil {
		return wiring.Report{}
	}
	var claude, skills, workflows []string
	for _, p := range paths {
		rel := strings.TrimPrefix(p, prefix)
		if !wiring.IsWiringPath(rel) {
			continue
		}
		switch {
		case strings.HasPrefix(rel, ".claude/skills/"):
			skills = append(skills, p)
		case strings.HasPrefix(rel, ".github/workflows/"):
			workflows = append(workflows, p)
		default:
			claude = append(claude, p)
		}
	}
	return wiring.Scan(wiring.Source{
		ClaudeMD:  claude,
		Skills:    skills,
		Workflows: workflows,
		Read: func(rel string) string {
			src, err := repo.ShowFile(ref, rel)
			if err != nil {
				return ""
			}
			return src
		},
	}, cfg)
}
