package adopt

// Peer attribution (ADR-0038).
//
// The defect this closes: a repo's prose cites paths that belong to a SIBLING
// repo, and `path-anchor` reported them as this repo's phantoms. It cannot
// tell "a path in my tree" from "a path in the repo I am writing about" — and
// it bites hardest on exactly the repos federation targets, siblings in one org
// documenting their shared boundary.
//
// When a peer IS configured (ADR-0032 `peers:`, ADR-0035 `org.hub`) and its
// checkout is on disk, the question has a better answer than "absent": the path
// lives over there. That is strictly more information than silence, and it is
// what makes the federation machinery earn its keep at adoption time.
//
// adopt still reads no `.nugit/` (ADR-0036 clause 1): the CALLER resolves peers
// and hands them in, so a repo that has never run `nugit init` passes none and
// the report falls back to history corroboration alone.

import (
	"os"
	"path"
	"strings"

	"github.com/n8o/nugit/internal/modelfacts"
)

// Peer is one sibling repo's checkout, as adopt sees it: a display name and a
// local directory. Never fetched, never written, never required.
type Peer struct {
	Name string
	Dir  string
}

// PeerStatus reports what each configured peer contributed. An absent peer is
// NOT an error (ADR-0032 clause 3) — CI normally checks out one repo — but it
// is reported, because a reader must be able to tell "no peer has this path"
// from "no peer was readable".
type PeerStatus struct {
	Name    string `json:"name"`
	Dir     string `json:"dir"`
	Present bool   `json:"present"`
	Units   int    `json:"units,omitempty"`
}

// peerRepo is a peer's checkout, probed lazily: its directory layout and the
// units its own detectors find. modelfacts.Units is the same inventory this
// report uses locally, so "lives in <peer>" means the same thing on both sides
// of the boundary.
type peerRepo struct {
	name  string
	tree  tree
	names map[string]bool // normalized peer unit names + dir basenames
}

// loadPeers reads each configured peer's checkout. A peer that is not on disk
// contributes nothing and says so; it never errors the report.
func loadPeers(in []Peer) (peers []*peerRepo, status []PeerStatus) {
	seen := map[string]bool{}
	for _, p := range in {
		name := strings.TrimSpace(p.Name)
		dir := strings.TrimSpace(p.Dir)
		if name == "" || dir == "" || seen[name] {
			continue
		}
		seen[name] = true
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			status = append(status, PeerStatus{Name: name, Dir: dir})
			continue
		}
		pr := &peerRepo{name: name, tree: newTree(dir), names: map[string]bool{}}
		units := modelfacts.Units(dir)
		for _, u := range units {
			pr.names[normalize(u.Name)] = true
			pr.names[normalize(path.Base(u.Dir))] = true
		}
		peers = append(peers, pr)
		status = append(status, PeerStatus{Name: name, Dir: dir, Present: true, Units: len(units)})
	}
	return peers, status
}

// holds reports the spelling of c that this peer's checkout contains, or "".
//
// The two tests are deliberately asymmetric, for the same reason the local
// absence veto is wider than the local quorum. A PATH the prose wrote is
// checked against the peer's tree directly: `libs/foo` in a document about the
// sibling is a claim about the sibling's layout, and finding it there is the
// whole point. A BARE NAME is only attributed when it names a DETECTED unit of
// the peer — a bare directory match at a peer's root would attribute on
// coincidences of layout (`docs`, `tools`), and a wrong attribution here is
// cheap but a noisy one is not.
func (p *peerRepo) holds(c *claim) string {
	for _, s := range c.Spellings {
		if strings.Contains(s, "/") && p.tree.dirExists(s) {
			return s
		}
	}
	for _, s := range c.Spellings {
		n := normalize(s)
		if p.names[n] || p.names[normalize(path.Base(n))] {
			return s
		}
	}
	return ""
}
