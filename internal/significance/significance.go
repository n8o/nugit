// Package significance classifies a PR into a disclosure tier (§9.4). Per the
// review and §13.6 it is a transparent heuristic, not an LLM call — every
// verdict carries the reasons that produced it, so a misfire is auditable.
package significance

import (
	"fmt"

	"github.com/burrowfarm/nugit/internal/model"
)

// Thresholds for the trivial tier. Tunable later via config.yml.
const (
	trivialMaxFiles = 2
	trivialMaxChurn = 20
)

// Classify returns the significance tier and the reasons behind it.
//
// undeclaredCrossEdge is true when the C4<->code check found a new
// cross-component dependency the model does not declare — a strong
// architectural signal computed before this call to avoid a dependency cycle.
func Classify(c4 model.C4Delta, code model.CodeDelta, know model.KnowledgeDelta, undeclaredCrossEdge bool) model.Significance {
	var reasons []string

	// --- architectural signals ---
	if !c4.Empty() {
		reasons = append(reasons, "C4 model (workspace.dsl) changed structurally")
	}
	if undeclaredCrossEdge {
		reasons = append(reasons, "code introduced a cross-component dependency not declared in the model")
	}
	if n := countKind(know, model.KindDecision); n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d decision record(s) added/changed", n))
	}
	if !c4.Empty() || undeclaredCrossEdge {
		return model.Significance{Tier: model.TierArchitectural, Reasons: reasons}
	}

	// --- feature signals ---
	comps := touchedComponents(code)
	if comps > 1 {
		reasons = append(reasons, fmt.Sprintf("changes span %d C4 components", comps))
	}
	if !know.Empty() {
		reasons = append(reasons, "knowledge objects (specs/lessons/decisions) changed")
	}
	if comps > 1 || !know.Empty() {
		return model.Significance{Tier: model.TierFeature, Reasons: reasons}
	}

	// --- trivial vs feature by size ---
	churn := code.TotalAdd + code.TotalDel
	if len(code.Files) <= trivialMaxFiles && churn <= trivialMaxChurn {
		reasons = append(reasons, fmt.Sprintf("small change: %d file(s), %d lines", len(code.Files), churn))
		return model.Significance{Tier: model.TierTrivial, Reasons: reasons}
	}
	reasons = append(reasons, fmt.Sprintf("non-trivial churn: %d file(s), %d lines", len(code.Files), churn))
	return model.Significance{Tier: model.TierFeature, Reasons: reasons}
}

func countKind(d model.KnowledgeDelta, k model.Kind) int {
	n := 0
	for _, c := range d.Changes {
		if c.Object != nil && c.Object.Type == k && (c.Status == "A" || c.Status == "M") {
			n++
		}
	}
	return n
}

func touchedComponents(code model.CodeDelta) int {
	set := map[string]bool{}
	for _, f := range code.Files {
		if f.Component != "" {
			set[f.Component] = true
		}
	}
	return len(set)
}
