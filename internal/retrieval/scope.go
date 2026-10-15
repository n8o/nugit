package retrieval

import (
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/mapping"
	"github.com/n8o/nugit/internal/model"
)

// ScopedDecisions returns the live (not superseded, not invalidated) local
// decisions that context(path) would admit with NO task — the same scope chain
// (component, parent container, global) and the same applies_to_paths binding
// Context uses, over a caller-supplied model and object set.
//
// It exists so a caller that has read the store and the model at a historical
// ref (the replay-task export, ADR-0044) can ask "what was in scope for this
// path back then" without a second copy of the scoping rule drifting from the
// one context() serves today. path is nugit-root-relative.
func ScopedDecisions(m model.Model, objs []model.KnowledgeObject, path string) []model.KnowledgeObject {
	comp := resolveComponent(mapping.New(m), path)
	parent := m.ContainerOf(comp)
	var out []model.KnowledgeObject
	for i := range objs {
		o := &objs[i]
		if o.Foreign() || o.Type != model.KindDecision || o.ID == "" {
			continue
		}
		if st := effectiveStatus(o); st == model.StatusSuperseded || st == model.StatusInvalidated {
			continue
		}
		if !knowledge.AppliesTo(o, path) && !inScope(o, comp, parent) {
			continue
		}
		out = append(out, *o)
	}
	return out
}
