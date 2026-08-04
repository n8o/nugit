package doctor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/config"
)

// landscapeHealth reports the org landscape (ADR-0034) advisorily: whether one
// was found and from WHERE, what it declares, and the two ways it can be
// quietly useless — an invalid `nugit_paths` glob (matches nothing, so the
// ownership check can never fire) and a `nugit_owner`/`nugit_repo` naming a
// repo id nothing this reader can see declares (a typo nothing else catches).
//
// It also surfaces the ambiguous case, which is the one thing resolution
// deliberately refuses to decide: two peers each declaring a landscape means
// the org has two writers for one fact (ADR-0011), and nugit reports that
// rather than picking silently.
//
// Never gating. A landscape is optional, an absent peer is the normal CI state,
// and modelling debt is a backlog item, not a setup failure.
func landscapeHealth(repoDir string, cfg config.Config) (bool, string) {
	dirs := []c4.LandscapeDir{{Dir: repoDir}}
	for _, p := range cfg.Peers {
		dirs = append(dirs, c4.LandscapeDir{Name: p.Name, Dir: p.Dir(repoDir), Hub: p.Hub})
	}
	res := c4.ResolveLandscape(c4.LandscapeSourcesFromDirs(dirs))

	if len(res.Ambiguous) > 0 {
		return false, fmt.Sprintf("ambiguous: peers %s each declare a landscape and this repo declares none — "+
			"NOTHING is used. Exactly one landscape is authoritative (ADR-0011): decide which repo owns %s "+
			"and delete the others, or declare one here",
			strings.Join(res.Ambiguous, ", "), c4.LandscapePath)
	}
	if !res.Found {
		return true, "no landscape.dsl found (local or from a peer) — org-level shared systems are not modelled"
	}

	l := res.Landscape
	shared, repos := 0, 0
	for _, s := range l.Systems {
		if s.Shared() {
			shared++
		}
		if s.Repo != "" {
			repos++
		}
	}
	where := "local " + l.Path
	if res.From != "" {
		where = "peer " + res.From + ":" + l.Path
	}
	head := fmt.Sprintf("landscape from %s: %d system(s), %d shared (owned by a named repo), %d declaring nugit_repo",
		where, len(l.Systems), shared, repos)

	var problems []string
	for _, bad := range c4.LandscapeInvalidGlobs(l) {
		problems = append(problems, fmt.Sprintf("system %q has an invalid nugit_paths glob %q (matches nothing, so ownership can never fire)",
			bad.System, bad.Pattern))
	}
	if dangling := danglingRepoIDs(repoDir, cfg, res); len(dangling) > 0 {
		problems = append(problems, fmt.Sprintf("repo id(s) nothing here declares: %s — check the spelling against each repo's `org.repo`",
			strings.Join(dangling, ", ")))
	}
	if len(problems) == 0 {
		return true, head
	}
	return false, head + "; " + strings.Join(problems, "; ")
}

// danglingRepoIDs returns the `nugit_owner` / `nugit_repo` values that no
// identity visible from here accounts for. Known ids are this repo's own
// `org.repo`, every reachable peer's `org.repo`, and every `nugit_repo` the
// landscape itself declares — the last one matters because the landscape is
// the org's roster, so a system declaring a repo id legitimises that id for
// owners even when the reader has no peer for it.
func danglingRepoIDs(repoDir string, cfg config.Config, res c4.LandscapeResolution) []string {
	known := map[string]bool{}
	if cfg.Org.Repo != "" {
		known[cfg.Org.Repo] = true
	}
	for _, p := range cfg.Peers {
		if pc, err := config.Load(p.Dir(repoDir)); err == nil && pc.Org.Repo != "" {
			known[pc.Org.Repo] = true
		}
	}
	for _, s := range res.Landscape.Systems {
		if s.Repo != "" {
			known[s.Repo] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range res.Landscape.Systems {
		for _, id := range []string{s.Repo, s.Owner} {
			if id == "" || known[id] || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
