// Package icepanel projects nugit's git-authoritative C4 model OUTBOUND into
// IcePanel (ADR-0011 / SPEC-002): git is the source of truth, IcePanel a derived,
// read-mostly view. Transform builds an IcePanel "Model as code" import payload;
// the CI job POSTs it with prune=true (full replace) so git deterministically
// overwrites IcePanel on every push and drift cannot accumulate.
//
// Schema note: the shapes below follow IcePanel's documented LandscapeImportData
// (modelObjects / modelConnections / tags). The exact object `type` and parenting
// must be validated against api.icepanel.io/v1/schemas/LandscapeImportData before
// the live push — see the Phase-1 open questions. Stable ids (the component handle)
// keep imports idempotent: re-importing an unchanged model produces no churn.
package icepanel

import (
	"sort"

	"github.com/n8o/nugit/internal/model"
)

// LandscapeImportData is the subset of IcePanel's import payload nugit emits.
type LandscapeImportData struct {
	ModelObjects     []ModelObject     `json:"modelObjects"`
	ModelConnections []ModelConnection `json:"modelConnections"`
}

// ModelObject is one node (a system or component) in the IcePanel model.
type ModelObject struct {
	ID       string   `json:"id"`                 // stable: nugit component handle (or "system")
	Handle   string   `json:"handle"`             // human id
	Name     string   `json:"name"`               // label
	Type     string   `json:"type"`               // "system" | "component"
	ParentID string   `json:"parentId,omitempty"` // components hang under the system
	Tech     string   `json:"tech,omitempty"`
	Tags     []string `json:"tags,omitempty"` // carries the paths globs so the binding survives
}

// ModelConnection is one directed dependency between objects.
type ModelConnection struct {
	ID       string `json:"id"` // stable: "src->dst"
	OriginID string `json:"originId"`
	TargetID string `json:"targetId"`
	Name     string `json:"name,omitempty"`
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
		ModelObjects: []ModelObject{{ID: rootID, Handle: rootID, Name: name, Type: "system"}},
	}

	comps := append([]model.Component(nil), m.Components...)
	sort.Slice(comps, func(i, j int) bool { return comps[i].ID < comps[j].ID })
	for _, c := range comps {
		label := c.Name
		if label == "" {
			label = c.ID
		}
		out.ModelObjects = append(out.ModelObjects, ModelObject{
			ID:       c.ID,
			Handle:   c.ID,
			Name:     label,
			Type:     "component",
			ParentID: rootID,
			Tech:     c.Tech,
			Tags:     append([]string(nil), c.Paths...), // keep the file->component binding visible
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
		out.ModelConnections = append(out.ModelConnections, ModelConnection{
			ID: id, OriginID: r.Src, TargetID: r.Dst, Name: r.Desc,
		})
	}
	return out
}
