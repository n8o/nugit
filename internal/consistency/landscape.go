package consistency

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// LandscapeOpts configures the org-landscape ownership check (ADR-0034). The
// zero value is INERT: with no declared identity this repo cannot know whether
// it is a system's owner, and ADR-0033 point 3 already settled that guessing an
// identity is strictly worse than having none.
type LandscapeOpts struct {
	// OrgRepo is this repo's stable org-wide id (config `org.repo`), compared
	// by string equality against a system's `nugit_owner`.
	OrgRepo string
	// Peers are the sibling checkouts a landscape may be read from when this
	// repo declares none (ADR-0032 transport). An absent peer contributes
	// nothing and can never error.
	Peers []knowledge.PeerSource
}

// checkLandscapeOwnership warns when this PR changes files that configure a
// landscape system some OTHER repo owns (ADR-0034 point 4).
//
// The whole point is that a repo's own model cannot express this: repo A's CI
// configuration legitimately lives in A's tree while the cluster it configures
// belongs to B, so no per-repo C4 element can carry the fact. The landscape's
// `nugit_paths` are evaluated against whichever repo is READING, which is what
// lets A's files match a system A does not own.
//
// Warn, never fail: the remediation is coordination with another team, often in
// a repo the author cannot write to. A repo that wants an enforceable two-sided
// invariant has contracts (ADR-0033).
func checkLandscapeOwnership(in Input) []model.Finding {
	me := in.Landscape.OrgRepo
	if me == "" {
		return nil // inert without identity — never guess which repo this is
	}
	res := resolveLandscape(in)
	if !res.Found {
		return nil
	}
	paths := make([]string, 0, len(in.Code.Files))
	for _, fc := range in.Code.Files {
		paths = append(paths, fc.Path)
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	matched := c4.ConfiguringAny(res.Landscape, paths)

	systems := append([]model.LandscapeSystem(nil), res.Landscape.Systems...)
	sort.Slice(systems, func(i, j int) bool { return systems[i].ID < systems[j].ID })
	var fs []model.Finding
	for _, s := range systems {
		// Only a SHARED system (one with a declared owner) can be owned
		// elsewhere, and a system this repo owns is this repo's business.
		if !s.Shared() || s.OwnedBy(me) {
			continue
		}
		hit := matched[s.ID]
		if len(hit) == 0 {
			continue
		}
		fs = append(fs, model.Finding{
			Check:    "landscape-ownership",
			Severity: model.SevWarn,
			Title: fmt.Sprintf("this PR configures %s, which %s owns",
				systemLabel(s), s.Owner),
			Detail: landscapeDetail(res, s, me, hit),
		})
	}
	return fs
}

// landscapeDetail words the finding so the reader knows what to DO: who owns
// the system, which of their infrastructure this PR is touching, and that the
// next step is coordinating with the owner — not that a rule tripped.
func landscapeDetail(res c4.LandscapeResolution, s model.LandscapeSystem, me string, hit []string) string {
	where := res.Landscape.Path
	if res.From != "" {
		where = "peer " + res.From + ":" + where
	}
	return fmt.Sprintf("the org landscape (%s) declares %s as owned by %s, and this PR changes %d file(s) "+
		"that configure it: %s. This repo is %s, not the owner — coordinate with %s before merging, "+
		"or land the change in the repo that owns the system.",
		where, systemLabel(s), s.Owner, len(hit), strings.Join(elide(hit, 5), ", "), me, s.Owner)
}

// systemLabel names a system the way a human reads it: its display name with
// its DSL id, so the reader can find it in landscape.dsl.
func systemLabel(s model.LandscapeSystem) string {
	if s.Name == "" || s.Name == s.ID {
		return fmt.Sprintf("%q", s.ID)
	}
	return fmt.Sprintf("%q (%s)", s.Name, s.ID)
}

// elide caps a path list so a finding stays readable when a PR touches a whole
// directory, while still saying how much was left out.
func elide(in []string, max int) []string {
	if len(in) <= max {
		return in
	}
	out := append([]string(nil), in[:max]...)
	return append(out, fmt.Sprintf("… and %d more", len(in)-max))
}

// resolveLandscape picks the ONE authoritative landscape for this repo's view
// (ADR-0034 point 3), reading this repo's own copy at the REVIEWED REF — never
// the working tree, per LESSON-read-from-reviewed-ref and ADR-0029 — and each
// peer's from its checkout, because this repo has no ref that addresses another
// repo's history (the same asymmetry ADR-0033 point 6 documents for contracts).
func resolveLandscape(in Input) c4.LandscapeResolution {
	var srcs []c4.LandscapeSource
	if src, err := in.Repo.ShowFile(in.Head, in.Prefix+c4.LandscapePath); err == nil && strings.TrimSpace(src) != "" {
		srcs = append(srcs, c4.LandscapeSource{Path: c4.LandscapePath, Src: src})
	}
	dirs := make([]c4.LandscapeDir, 0, len(in.Landscape.Peers))
	for _, p := range in.Landscape.Peers {
		dirs = append(dirs, c4.LandscapeDir{Name: p.Name, Dir: p.Dir, Hub: p.Hub})
	}
	srcs = append(srcs, c4.LandscapeSourcesFromDirs(dirs)...)
	return c4.ResolveLandscape(srcs)
}
