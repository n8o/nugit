// Package overlap finds the open work that touches what a branch touches
// (ADR-0065): the same files, the same C4 components, or the same store keys.
// Two branches that both add ADR-0055 each pass every check alone and collide
// only once both have merged; two that rewrite one function conflict only for
// whoever merges second. Nothing in a single PR's view can show either.
//
// Compute is a pure function of git refs. Gather is the edge that decides
// which refs count as open work: it asks gh for open pull requests and reads
// the remote branches already fetched, and fetches nothing itself.
package overlap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
)

// Input is what Compute compares.
type Input struct {
	Repo gitutil.Repo
	// Prefix is the nugit root within the git repo ("" when they coincide).
	Prefix string
	// Base is the target branch. A change is what lies between its merge base
	// with Base (or with the work's own Base) and its tip.
	Base string
	// Head is the branch to compare the open work with; "" compares only the
	// open work's pairs.
	Head string
	// Model names the components; files map to them through its path globs.
	// One model maps every change, so "the same component" means one thing.
	Model  model.Model
	Others []model.OpenWork
	// Pairs also compares every two pieces of open work with each other.
	Pairs bool
}

// Compute compares Head with each piece of open work, and with Pairs every
// two of them. Work on Head's own line (an ancestor of it, or built on it,
// as a stack is) is not open work against it and is left out. The view's
// Source and Unread are the caller's to fill.
func Compute(in Input) (model.OverlapView, error) {
	c := computer{in: in, mp: mapping.New(in.Model), names: names(in.Model), fps: map[string]footprint{}}
	var v model.OverlapView
	if in.Head != "" {
		head, err := c.footprint(in.Base, in.Head)
		if err != nil {
			return v, err
		}
		for _, w := range in.Others {
			if c.sameLine(w.Ref, in.Head) {
				continue
			}
			fp, err := c.footprint(c.baseOf(w), w.Ref)
			if err != nil {
				return v, err
			}
			v.Checked++
			if o := c.compare(head, fp, in.Head, w.Ref); !o.Empty() {
				v.Items = append(v.Items, model.OverlapItem{With: w, Overlap: o})
			}
		}
		sortItems(v.Items)
	}
	if in.Pairs {
		v.Pairwise = true
		if in.Head == "" {
			v.Checked = len(in.Others)
		}
		for i := range in.Others {
			for j := i + 1; j < len(in.Others); j++ {
				a, b := in.Others[i], in.Others[j]
				if c.sameLine(a.Ref, b.Ref) {
					continue
				}
				fa, err := c.footprint(c.baseOf(a), a.Ref)
				if err != nil {
					return v, err
				}
				fb, err := c.footprint(c.baseOf(b), b.Ref)
				if err != nil {
					return v, err
				}
				if o := c.compare(fa, fb, a.Ref, b.Ref); !o.Empty() {
					v.Pairs = append(v.Pairs, model.OverlapPair{A: a, B: b, Overlap: o})
				}
			}
		}
		sortPairs(v.Pairs)
	}
	return v, nil
}

type computer struct {
	in    Input
	mp    *mapping.Mapper
	names map[string]string
	fps   map[string]footprint
}

// footprint is what one change touches.
type footprint struct {
	files   map[string]bool
	comps   map[string]bool
	records []record
}

// record is a store record a change adds.
type record struct {
	id, path string
	kind     model.Kind
	key      knowledge.NumberKey
	numbered bool
}

func (c *computer) baseOf(w model.OpenWork) string {
	if w.Base != "" {
		return w.Base
	}
	return c.in.Base
}

// sameLine reports whether one commit contains the other: a branch and the
// one stacked on it, or a branch and its own older push.
func (c *computer) sameLine(a, b string) bool {
	return c.in.Repo.IsAncestor(a, b) || c.in.Repo.IsAncestor(b, a)
}

// footprint reads what tip changes since its merge base with base. Measured
// from the merge base, not from base: anything base gained after the branch
// was cut is not the branch's change.
func (c *computer) footprint(base, tip string) (footprint, error) {
	key := base + "\x00" + tip
	if fp, ok := c.fps[key]; ok {
		return fp, nil
	}
	repo := c.in.Repo
	from := repo.MergeBase(base, tip)
	changes, err := repo.NameStatus(from, tip)
	if err != nil {
		return footprint{}, fmt.Errorf("reading %s: %w", tip, err)
	}
	fp := footprint{files: map[string]bool{}, comps: map[string]bool{}}
	nugitDir := c.in.Prefix + ".nugit/"
	for _, ch := range changes {
		fp.files[ch.Path] = true
		if comp := c.mp.Resolve(ch.Path); comp != "" {
			fp.comps[comp] = true
		}
		if ch.Status != "A" || !strings.HasPrefix(ch.Path, nugitDir) || !strings.HasSuffix(ch.Path, ".md") {
			continue
		}
		src, err := repo.ShowFile(tip, ch.Path)
		if err != nil {
			continue
		}
		obj, ok := knowledge.ParseObject(ch.Path, src)
		if !ok || obj.ID == "" {
			continue
		}
		r := record{id: obj.ID, path: ch.Path, kind: obj.Type}
		r.key, r.numbered = knowledge.NumberKeyOf("", obj.ID, ch.Path)
		fp.records = append(fp.records, r)
	}
	c.fps[key] = fp
	return fp, nil
}

// compare is what two footprints share. When they share files, the two tips
// are merged in simulation, so the view can tell a file both edit from a file
// whoever merges second will have to resolve.
func (c *computer) compare(a, b footprint, aRef, bRef string) model.Overlap {
	var o model.Overlap
	for f := range a.files {
		if b.files[f] {
			o.Files = append(o.Files, f)
		}
	}
	sort.Strings(o.Files)
	for id := range a.comps {
		if b.comps[id] {
			o.Components = append(o.Components, c.names[id])
		}
	}
	sort.Strings(o.Components)
	o.Collisions = collisions(a.records, b.records)
	if len(o.Files) > 0 {
		if conflicts, ok := c.in.Repo.MergeConflicts(aRef, bRef); ok {
			o.MergeChecked = true
			shared := map[string]bool{}
			for _, f := range o.Files {
				shared[f] = true
			}
			// A conflict outside the shared files comes from the target
			// moving under one side, not from the two changes meeting.
			for _, f := range conflicts {
				if shared[f] {
					o.Conflicts = append(o.Conflicts, f)
				}
			}
		}
	}
	return o
}

// collisions lists the store keys both changes add: one record id, or one
// decision number under one directory and id namespace (the key the
// duplicate-decision-number check enforces once both have merged).
func collisions(a, b []record) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, ra := range a {
		for _, rb := range b {
			switch {
			case ra.id == rb.id && ra.path == rb.path:
				add(fmt.Sprintf("both add %s as `%s`", ra.id, ra.path))
			case ra.id == rb.id:
				add(fmt.Sprintf("both add %s: `%s` here, `%s` there", ra.id, ra.path, rb.path))
			case ra.numbered && rb.numbered && ra.key == rb.key &&
				ra.kind == model.KindDecision && rb.kind == model.KindDecision:
				add(fmt.Sprintf("both take decision number %s in `%s`: %s here, %s there",
					ra.key.Number, ra.key.Dir, ra.id, rb.id))
			}
		}
	}
	sort.Strings(out)
	return out
}

// names maps each component and container id to its display name.
func names(m model.Model) map[string]string {
	n := map[string]string{}
	for _, ct := range m.Containers {
		n[ct.ID] = ct.ID
		if ct.Name != "" {
			n[ct.ID] = ct.Name
		}
	}
	for _, c := range m.Components {
		n[c.ID] = c.ID
		if c.Name != "" {
			n[c.ID] = c.Name
		}
	}
	return n
}

// weight orders overlaps: a store-key collision first (it breaks main once
// both merge), then a merge conflict, then shared files, then components.
func weight(o model.Overlap) [4]int {
	return [4]int{len(o.Collisions), len(o.Conflicts), len(o.Files), len(o.Components)}
}

func heavier(a, b model.Overlap) (bool, bool) {
	wa, wb := weight(a), weight(b)
	for i := range wa {
		if wa[i] != wb[i] {
			return wa[i] > wb[i], true
		}
	}
	return false, false
}

func less(a, b model.OpenWork) bool {
	if (a.PR > 0) != (b.PR > 0) {
		return a.PR > 0
	}
	if a.PR != b.PR {
		return a.PR < b.PR
	}
	return a.Branch < b.Branch
}

func sortItems(items []model.OverlapItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if h, ok := heavier(items[i].Overlap, items[j].Overlap); ok {
			return h
		}
		return less(items[i].With, items[j].With)
	})
}

func sortPairs(ps []model.OverlapPair) {
	sort.SliceStable(ps, func(i, j int) bool {
		if h, ok := heavier(ps[i].Overlap, ps[j].Overlap); ok {
			return h
		}
		if ps[i].A != ps[j].A {
			return less(ps[i].A, ps[j].A)
		}
		return less(ps[i].B, ps[j].B)
	})
}
