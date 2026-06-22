// Package icepanel projects nugit's git-authoritative C4 model OUTBOUND into
// IcePanel (ADR-0011 / SPEC-002): git is the source of truth, IcePanel a derived,
// read-mostly view. Transform builds an IcePanel "Model as code" import payload;
// the CI job POSTs it with prune=true (full replace) so git deterministically
// overwrites IcePanel on every push and drift cannot accumulate.
//
// Schema validated live against the IcePanel API (2026-06-19):
//   - modelConnections REQUIRE `direction` ("outgoing"|"bidirectional") + name/
//     originId/targetId/id; modelObjects REQUIRE id/name/type (type ∈ actor|app|
//     component|group|store|system — "root" is NOT importable).
//   - the paths globs ride in `description` (objects have tagIds/technologyIds
//     references, not free-string tags).
//
// Stable ids (the component handle) keep imports idempotent (no churn on re-import).
//
// Validated live: this payload is ACCEPTED (HTTP 200) and follows IcePanel's
// documented parent rules. But the import APPLY (async) proved opaque over raw HTTP
// — a hierarchy-correct payload still did not fully populate (the imported top-level
// domain collapses into a duplicate root and deeper objects are dropped), and the
// import-job error endpoint is unavailable to diagnose. CONCLUSION: nugit's job ends
// at producing this validated export; the PUSH should use IcePanel's OFFICIAL SDK /
// GitHub Action (which owns the async import lifecycle), not a hand-rolled HTTP push.
// (An earlier "import is paid-gated on the trial" note was WRONG — it's the apply
// lifecycle, not a plan limit.)
package icepanel

import (
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// LandscapeImportData is the subset of IcePanel's import payload nugit emits.
type LandscapeImportData struct {
	ModelObjects     []ModelObject     `json:"modelObjects"`
	ModelConnections []ModelConnection `json:"modelConnections"`
}

// ModelObject is one node (a system or component) in the IcePanel model. Fields
// match IcePanel's ModelObjectImport schema (id/name/type required; type ∈
// actor|app|component|group|root|store|system). The paths globs ride in
// description so the file→component binding survives the projection.
type ModelObject struct {
	ID          string `json:"id"` // stable: nugit component handle (or the system id)
	Name        string `json:"name"`
	Type        string `json:"type"`
	ParentID    string `json:"parentId,omitempty"` // components hang under the system
	Description string `json:"description,omitempty"`
}

// ModelConnection is one directed dependency. Fields match ModelConnectionImport
// (id/name/originId/targetId/direction required; direction ∈ outgoing|bidirectional).
type ModelConnection struct {
	ID        string `json:"id"` // stable: "src->dst"
	Name      string `json:"name"`
	OriginID  string `json:"originId"`
	TargetID  string `json:"targetId"`
	Direction string `json:"direction"` // "outgoing" for a directed dependency
}

// IcePanel parent rules (validated against the live API reference): domain → system
// → app/store → component (a domain is the only top-level type; a component must
// hang under an app, never directly under a system). nugit is one binary, so it maps
// to a domain→system→app spine, with its packages as components under the app.
const (
	domainID = "nugit-domain"
	systemID = "nugit-system"
	appID    = "nugit-app"
)

// Transform maps a parsed C4 model to a LandscapeImportData payload. Deterministic
// (sorted) and idempotent (handle-stable ids) so prune=true re-imports don't churn.
func Transform(m model.Model) LandscapeImportData {
	name := m.Name
	if name == "" {
		name = "nugit"
	}
	out := LandscapeImportData{ModelObjects: []ModelObject{
		{ID: domainID, Name: name, Type: "domain"}, // top-level (no parent)
		{ID: systemID, Name: name, Type: "system", ParentID: domainID},
		{ID: appID, Name: name, Type: "app", ParentID: systemID}, // the binary; components hang here
	}}

	comps := append([]model.Component(nil), m.Components...)
	sort.Slice(comps, func(i, j int) bool { return comps[i].ID < comps[j].ID })
	for _, c := range comps {
		label := c.Name
		if label == "" {
			label = c.ID
		}
		desc := strings.Join(c.Paths, ", ") // keep the file->component binding visible
		if c.Tech != "" {
			desc = c.Tech + " — " + desc
		}
		out.ModelObjects = append(out.ModelObjects, ModelObject{
			ID:          c.ID,
			Name:        label,
			Type:        "component",
			ParentID:    appID,
			Description: desc,
		})
	}

	rels := append([]model.Relationship(nil), m.Relationships...)
	sort.Slice(rels, func(i, j int) bool {
		if rels[i].Src != rels[j].Src {
			return rels[i].Src < rels[j].Src
		}
		return rels[i].Dst < rels[j].Dst
	})
	seen := map[string]bool{}
	for _, r := range rels {
		id := r.Src + "->" + r.Dst
		if seen[id] {
			continue
		}
		seen[id] = true
		name := r.Desc
		if name == "" {
			name = "depends on" // name is required + must be meaningful
		}
		out.ModelConnections = append(out.ModelConnections, ModelConnection{
			ID: id, Name: name, OriginID: r.Src, TargetID: r.Dst, Direction: "outgoing",
		})
	}
	return out
}
