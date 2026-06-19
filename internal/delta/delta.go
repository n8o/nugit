// Package delta computes the four deterministic PR deltas (§9.2). No LLM, no
// network: every value here is a pure function of two git refs and the committed
// artifacts between them.
package delta

import (
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
)

// DefaultDSLPath is the canonical location of the C4 model.
const DefaultDSLPath = ".nugit/architecture/workspace.dsl"

// C4 parses workspace.dsl at base and head and returns the structural delta plus
// both parsed models (the head model drives the file->component mapping).
func C4(repo gitutil.Repo, base, head, dslPath string) (model.C4Delta, model.Model, model.Model, error) {
	baseSrc, err := repo.ShowFile(base, dslPath)
	if err != nil {
		return model.C4Delta{}, model.Model{}, model.Model{}, err
	}
	headSrc, err := repo.ShowFile(head, dslPath)
	if err != nil {
		return model.C4Delta{}, model.Model{}, model.Model{}, err
	}
	baseModel := c4.Parse(baseSrc)
	headModel := c4.Parse(headSrc)
	raw, _ := repo.RawDiff(base, head, dslPath)
	return c4.Diff(baseModel, headModel, raw), baseModel, headModel, nil
}

// Code computes the diff summary grouped by C4 component. Knowledge files under
// the nugit root's .nugit/ are excluded — they belong to the knowledge delta.
// prefix is the nugit root within the git repo ("" when they coincide).
func Code(repo gitutil.Repo, base, head string, mp *mapping.Mapper, prefix string) (model.CodeDelta, error) {
	changes, err := repo.NameStatus(base, head)
	if err != nil {
		return model.CodeDelta{}, err
	}
	counts, err := repo.Numstat(base, head)
	if err != nil {
		return model.CodeDelta{}, err
	}
	nugitDir := prefix + ".nugit/"
	d := model.CodeDelta{ByComp: map[string][]model.FileChange{}}
	for _, fc := range changes {
		if strings.HasPrefix(fc.Path, nugitDir) {
			continue
		}
		if c, ok := counts[fc.Path]; ok {
			fc.Added, fc.Deleted = c[0], c[1]
		}
		fc.Component = mp.Resolve(fc.Path)
		d.Files = append(d.Files, fc)
		d.ByComp[fc.Component] = append(d.ByComp[fc.Component], fc)
		d.TotalAdd += fc.Added
		d.TotalDel += fc.Deleted
	}
	sort.Slice(d.Files, func(i, j int) bool { return d.Files[i].Path < d.Files[j].Path })
	return d, nil
}

// Knowledge computes which durable knowledge objects the PR added/changed/removed.
// prefix is the nugit root within the git repo ("" when they coincide).
func Knowledge(repo gitutil.Repo, base, head, prefix string) (model.KnowledgeDelta, error) {
	changes, err := repo.NameStatus(base, head)
	if err != nil {
		return model.KnowledgeDelta{}, err
	}
	nugitDir := prefix + ".nugit/"
	var d model.KnowledgeDelta
	for _, fc := range changes {
		if !strings.HasPrefix(fc.Path, nugitDir) || !strings.HasSuffix(fc.Path, ".md") {
			continue
		}
		kc := model.KnowledgeChange{Path: fc.Path, Status: fc.Status}
		ref := head
		if fc.Status == "D" {
			ref = base
		}
		content, _ := repo.ShowFile(ref, fc.Path)
		if obj, ok := knowledge.ParseObject(fc.Path, content); ok {
			kc.Object = obj
		}
		d.Changes = append(d.Changes, kc)
	}
	sort.Slice(d.Changes, func(i, j int) bool { return d.Changes[i].Path < d.Changes[j].Path })
	return d, nil
}
