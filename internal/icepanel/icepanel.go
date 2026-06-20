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
// PUSH-TIME requirement discovered live (not encodable in this pure transform):
// the landscape's root object is system-managed and not importable, so the push
// step must GET the root id and remap the system's ParentID to it before POSTing.
// On a FREE/trial plan the import endpoint returns 200 but the async apply does
// not populate the model — import is a paid feature; full apply/diagram/snapshot
// validation needs a paid IcePanel plan.
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

// rootID is the synthetic system object every component is parented to.
const rootID = "nugit-system"

// Transform maps a parsed C4 model to a LandscapeImportData payload. Deterministic
// (sorted) and idempotent (handle-stable ids) so prune=true re-imports don't churn.
func Transform(m model.Model) LandscapeImportData {
	name := m.Name
	if name == "" {
		name = "system"
	}
	out := LandscapeImportData{
		ModelObjects: []ModelObject{{ID: rootID, Name: name, Type: "system"}},
	}

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
			ParentID:    rootID,
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
