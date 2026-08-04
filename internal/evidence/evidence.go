// Package evidence derives the trust tier of each knowledge object — the
// "nothing is labeled trusted on faith" surface. A tier states how much of an
// object nugit mechanically verifies: enforced (governed components are
// path-bound AND the edge checks fail on violations), checked (bound but not
// enforced), declared (nothing binds it to code), proposed (candidate lane,
// ADR-0016), stale (superseded/invalidated). Derived at read time, like
// EffectiveStatus — never authored, never persisted.
//
// Honesty note: "enforced" claims the governance SUBSTRATE is verified — the
// c4<->code checks catch undeclared cross-component dependencies touching the
// object's components. It does not claim the object's prose constraint is
// itself proven.
package evidence

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/n8o/nugit/internal/bootstrap"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/tsdeps"
)

// Signals are the repo-level facts the tier derivation composes.
type Signals struct {
	// Model is the parsed C4 workspace at the reviewed ref.
	Model model.Model
	// Enforce is true when c4.mode is enforce (edge checks fail, not warn).
	Enforce bool
	// Backend is true when an edge-check language backend is active (Go
	// module, CMake, Python, or TS with dependency-cruiser installed).
	Backend bool
}

// TierOf derives one object's tier. Precedence: stale beats proposed beats
// the binding-derived tiers — a superseded draft is dead, not pending.
func TierOf(o model.KnowledgeObject, s Signals) model.Evidence {
	if o.EffectiveStatus == model.StatusSuperseded || o.EffectiveStatus == model.StatusInvalidated {
		return model.EvidenceStale
	}
	if o.Status == model.StatusProposed {
		return model.EvidenceProposed
	}
	comps := GovernedComponents(o)
	// A direct path binding (applies_to_paths, ADR-0020) counts as a binding:
	// the object is tied to code, so it is never merely declared.
	direct := len(o.AppliesToPaths) > 0
	bound := 0
	for _, id := range comps {
		if pathBound(s.Model, id) {
			bound++
		}
	}
	if bound == 0 && !direct {
		return model.EvidenceDeclared
	}
	// Glob VALIDITY is not re-checked here — an invalid glob is already a
	// model-health warning; the tier trusts the binding it can see.
	//
	// A direct path binding caps the tier at checked: it is verified only by
	// the warn-severity stale-knowledge check, never by a fail-severity edge
	// check, so an object declaring applies_to_paths is not fully enforced —
	// mirroring the partially-bound component rule.
	if !direct && bound == len(comps) && s.Enforce && s.Backend && !s.Model.Structural() {
		return model.EvidenceEnforced
	}
	return model.EvidenceChecked
}

// pathBound reports whether a governed element id is bound to files: a
// component with paths, a container with its own paths, or a container with at
// least one path-bound child component. Flat models see only the first case —
// behavior unchanged.
func pathBound(m model.Model, id string) bool {
	if c, ok := m.Comp(id); ok {
		return len(c.Paths) > 0
	}
	ct, ok := m.Container(id)
	if !ok {
		return false
	}
	if len(ct.Paths) > 0 {
		return true
	}
	for _, c := range m.Components {
		if c.Container == id && len(c.Paths) > 0 {
			return true
		}
	}
	return false
}

// Annotate populates Evidence on every object in place (mirrors
// knowledge.ResolveEffectiveStatus). Untyped objects (no id) are left
// un-annotated; renderers omit an empty tier.
func Annotate(objs []model.KnowledgeObject, s Signals) {
	for i := range objs {
		if objs[i].ID == "" {
			continue
		}
		objs[i].Evidence = TierOf(objs[i], s)
	}
}

// AnnotateDelta copies tiers onto the delta's objects from the fully-resolved
// head set (delta objects are parsed standalone, so their EffectiveStatus
// lacks cross-set supersede resolution). Objects absent from the head set
// (deleted at head) fall back to a standalone derivation.
func AnnotateDelta(d *model.KnowledgeDelta, resolved map[string]model.Evidence, s Signals) {
	for i := range d.Changes {
		o := d.Changes[i].Object
		if o == nil || o.ID == "" {
			continue
		}
		if tier, ok := resolved[o.ID]; ok {
			o.Evidence = tier
			continue
		}
		o.Evidence = TierOf(*o, s)
	}
}

// Tiers indexes Annotate results by object id, for AnnotateDelta.
func Tiers(objs []model.KnowledgeObject) map[string]model.Evidence {
	out := make(map[string]model.Evidence, len(objs))
	for _, o := range objs {
		if o.ID != "" {
			out[o.ID] = o.Evidence
		}
	}
	return out
}

// GovernedComponents returns the C4 component ids an object governs, via its
// scope and via constrains:/affects:/governs: edges. (Moved verbatim from
// internal/consistency so the definition lives once.)
func GovernedComponents(o model.KnowledgeObject) []string {
	set := map[string]bool{}
	if o.Scope != "" && o.Scope != "global" {
		set[o.Scope] = true
	}
	for _, e := range o.RelatesTo {
		edge := knowledge.ParseEdge(e)
		switch edge.Relation {
		case "constrains", "affects", "governs":
			set[edge.Target] = true
		}
	}
	var out []string
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// BackendActive reports whether an edge-check language backend would run for
// this repo: a Go module, CMake, Python, or TS with dependency-cruiser
// actually installed (an uninstalled cruiser degrades enforcement, matching
// the ts<->code info finding).
func BackendActive(repoDir string) bool {
	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err == nil {
		return true
	}
	if bootstrap.DetectCMake(repoDir) || bootstrap.DetectPython(repoDir) {
		return true
	}
	return bootstrap.DetectTS(repoDir) && tsdeps.Available()
}
