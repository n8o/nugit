package knowledge

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/n8o/nugit/internal/model"
)

// PeerSource is one configured peer store: a short display namespace and the
// local checkout it lives in (ADR-0032, phase 1 — local paths only, never
// fetched, never written).
type PeerSource struct {
	Name string // the origin namespace: [a-z0-9-], unique across peers
	Dir  string // nugit root of the peer checkout (already resolved absolute-or-relative)
}

// PeerLoad reports what one peer contributed to a merged set. It exists so an
// absent peer is VISIBLE (doctor, `-format json`) instead of silently empty:
// "the sibling isn't checked out" and "the sibling has nothing to say" are
// different facts, and CI routinely produces the first.
type PeerLoad struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
	// Reachable is true when the peer's .nugit/ directory could be read.
	Reachable bool `json:"reachable"`
	// Count is how many objects the peer contributed (0 when unreachable).
	Count int `json:"count"`
	// Note explains a zero contribution in one human sentence.
	Note string `json:"note,omitempty"`
}

// LoadPeer reads ONE peer's .nugit/ tree read-only and stamps src.Name as the
// Origin of every object. The peer's own `peers:` block is deliberately never
// read: federation is one level deep, which is also the cycle guard — A→B→A
// terminates because B's peers are not followed (ADR-0032).
//
// A missing or unreadable peer path is NOT an error. CI usually has only the
// repo under review checked out, and no absent sibling may ever fail a
// pr-render; the peer simply contributes nothing and says so in PeerLoad.
func LoadPeer(src PeerSource) ([]model.KnowledgeObject, PeerLoad) {
	pl := PeerLoad{Name: src.Name, Dir: src.Dir}
	fi, err := os.Stat(filepath.Join(src.Dir, ".nugit"))
	if err != nil || !fi.IsDir() {
		pl.Note = "not checked out here — no .nugit/ at " + src.Dir
		return nil, pl
	}
	objs, err := walkStore(src.Dir, src.Name)
	if err != nil {
		// Degrade, never propagate: an unreadable peer is the same class of
		// event as an absent one.
		pl.Note = "unreadable: " + err.Error()
		return nil, pl
	}
	// Resolve the peer's derivations WITHIN the peer set, before any merge.
	// Combined with the (origin, id) keying in the resolvers themselves, a
	// foreign supersedes/amends/reinforces edge can never reach a local object.
	resolve(objs)
	pl.Reachable, pl.Count = true, len(objs)
	if len(objs) == 0 {
		pl.Note = "reachable but empty"
	}
	return objs, pl
}

// LoadWithPeers is the peer-aware entry point: this repo's objects (identical to
// Load) followed by every reachable peer's, each foreign object carrying its
// peer name as Origin. Only the LOCAL store can fail the call — a peer never
// can (ADR-0032).
//
// Peers are loaded in configured order and each peer's objects are appended as a
// block, so the result is deterministic. Callers that merge must key identity on
// (Origin, ID); see Key.
func LoadWithPeers(repoDir string, peers []PeerSource) ([]model.KnowledgeObject, []PeerLoad, error) {
	objs, err := Load(repoDir)
	if err != nil {
		return nil, nil, err
	}
	self, _ := filepath.Abs(repoDir)
	var loads []PeerLoad
	for _, src := range peers {
		if abs, e := filepath.Abs(src.Dir); e == nil && abs == self {
			// A peer pointing at this repo would duplicate the whole local
			// store under a foreign namespace. Fail closed, visibly.
			loads = append(loads, PeerLoad{Name: src.Name, Dir: src.Dir,
				Note: "peer path resolves to this repo — skipped"})
			continue
		}
		po, pl := LoadPeer(src)
		objs = append(objs, po...)
		loads = append(loads, pl)
	}
	return objs, loads, nil
}

// PeerOrigins lists the origins present in a merged set, sorted — the cheap way
// for a renderer or a test to ask "did any peer contribute here?".
func PeerOrigins(objs []model.KnowledgeObject) []string {
	seen := map[string]bool{}
	var out []string
	for _, o := range objs {
		if o.Origin != "" && !seen[o.Origin] {
			seen[o.Origin] = true
			out = append(out, o.Origin)
		}
	}
	sort.Strings(out)
	return out
}
