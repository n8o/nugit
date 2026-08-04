package doctor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// orgHub reports the organization hub designation (ADR-0035): whether one is
// declared, whether it resolves to a configured peer, whether that peer is
// actually checked out here, and what it holds.
//
// Advisory and never gating, like every other federation surface. The three ways
// a hub can be useless are each a DISTINCT sentence, because the remediation
// differs and a single "no hub" would hide two of them:
//
//   - nothing declared        → the org has no canonical store yet (fine)
//   - declared, no such peer  → a typo, or a peers: entry that was removed
//   - declared, not checked out → the normal CI state; promote will refuse and
//     say so, and reading degrades exactly like any absent peer (ADR-0032)
func orgHub(repoDir string, cfg config.Config) (bool, string) {
	if cfg.Org.Hub == "" {
		if len(cfg.Peers) == 0 {
			return true, "no org hub designated (and no peers configured) — knowledge stays repo-local"
		}
		return true, fmt.Sprintf("no org hub designated — %d peer(s) are read peer-to-peer; set `org.hub: <peer-name>` to give the org a canonical store (ADR-0035)", len(cfg.Peers))
	}
	hub, ok := cfg.HubPeer()
	if !ok {
		names := make([]string, 0, len(cfg.Peers))
		for _, p := range cfg.Peers {
			names = append(names, p.Name)
		}
		sort.Strings(names)
		have := "this repo configures no peers"
		if len(names) > 0 {
			have = "configured peers: " + strings.Join(names, ", ")
		}
		return false, fmt.Sprintf("org.hub is %q, which is not one of the configured peers (%s) — the hub is a PEER with a role, so it must appear in `peers:` first; nothing fails, but promotion and hub-wins landscape resolution are inert",
			cfg.Org.Hub, have)
	}
	dir := hub.Dir(repoDir)
	objs, load := knowledge.LoadPeer(knowledge.PeerSource{Name: hub.Name, Dir: dir, Hub: true})
	if !load.Reachable {
		return false, fmt.Sprintf("hub %q is not checked out here (%s) — it degrades like any absent peer: reads contribute nothing and `nugit promote` refuses with the path it wanted",
			hub.Name, load.Note)
	}
	detail := fmt.Sprintf("hub %q at %s: %d object(s), %d global+ratified", hub.Name, hub.Path, len(objs), federatable(objs))
	if pending := proposedCount(objs); pending > 0 {
		detail += fmt.Sprintf(", %d proposed awaiting the hub owner's ratification", pending)
	}
	if cfg.Org.Repo == "" {
		return false, detail + " — but this repo declares no `org.repo`, so `nugit promote` cannot stamp where a record came from and will refuse (ADR-0033: never guess an identity)"
	}
	return true, detail
}

// proposedCount counts the hub's candidate lane — promoted records land there
// (ADR-0035 point 2) and stay until the hub owner ratifies, so a growing number
// is the org's promotion backlog.
func proposedCount(objs []model.KnowledgeObject) int {
	n := 0
	for _, o := range objs {
		if o.ID != "" && o.EffectiveStatus == model.StatusProposed {
			n++
		}
	}
	return n
}
