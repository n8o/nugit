package c4

import (
	"sort"

	"github.com/n8o/nugit/internal/model"
)

// Diff computes the structural delta between two parsed models (base -> head).
// rawDiff is the unified text diff of the DSL file, attached for display.
func Diff(base, head model.Model, rawDiff string) model.C4Delta {
	d := model.C4Delta{RawDiff: rawDiff}

	baseComp := indexComponents(base)
	headComp := indexComponents(head)

	for id, hc := range headComp {
		bc, ok := baseComp[id]
		if !ok {
			d.AddedComponents = append(d.AddedComponents, hc)
			continue
		}
		if fields := componentChangedFields(bc, hc); len(fields) > 0 {
			d.ChangedComponents = append(d.ChangedComponents, model.ComponentChange{
				Before: bc, After: hc, Fields: fields,
			})
		}
	}
	for id, bc := range baseComp {
		if _, ok := headComp[id]; !ok {
			d.RemovedComponents = append(d.RemovedComponents, bc)
		}
	}

	baseRel := indexRels(base)
	headRel := indexRels(head)
	for k, r := range headRel {
		if br, ok := baseRel[k]; !ok {
			d.AddedRels = append(d.AddedRels, r)
		} else if br.Desc != r.Desc {
			d.ChangedRels = append(d.ChangedRels, model.RelationshipChange{Before: br, After: r})
		}
	}
	for k, r := range baseRel {
		if _, ok := headRel[k]; !ok {
			d.RemovedRels = append(d.RemovedRels, r)
		}
	}

	sortComponents(d.AddedComponents)
	sortComponents(d.RemovedComponents)
	sort.Slice(d.ChangedComponents, func(i, j int) bool {
		return d.ChangedComponents[i].After.ID < d.ChangedComponents[j].After.ID
	})
	sortRels(d.AddedRels)
	sortRels(d.RemovedRels)
	sort.Slice(d.ChangedRels, func(i, j int) bool {
		a, b := d.ChangedRels[i].After, d.ChangedRels[j].After
		if a.Src != b.Src {
			return a.Src < b.Src
		}
		return a.Dst < b.Dst
	})
	return d
}

func indexComponents(m model.Model) map[string]model.Component {
	out := map[string]model.Component{}
	for _, c := range m.Components {
		out[c.ID] = c
	}
	return out
}

func indexRels(m model.Model) map[string]model.Relationship {
	out := map[string]model.Relationship{}
	for _, r := range m.Relationships {
		out[r.Src+"->"+r.Dst] = r
	}
	return out
}

func componentChangedFields(a, b model.Component) []string {
	var fields []string
	if a.Name != b.Name {
		fields = append(fields, "name")
	}
	if a.Tech != b.Tech {
		fields = append(fields, "technology")
	}
	if !eqSet(a.Paths, b.Paths) {
		fields = append(fields, "paths")
	}
	if !eqSet(a.Tags, b.Tags) {
		fields = append(fields, "tags")
	}
	return fields
}

func eqSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	am := map[string]int{}
	for _, x := range a {
		am[x]++
	}
	for _, x := range b {
		am[x]--
	}
	for _, v := range am {
		if v != 0 {
			return false
		}
	}
	return true
}

func sortComponents(cs []model.Component) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID })
}

func sortRels(rs []model.Relationship) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Src != rs[j].Src {
			return rs[i].Src < rs[j].Src
		}
		return rs[i].Dst < rs[j].Dst
	})
}
