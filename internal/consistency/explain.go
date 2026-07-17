package consistency

import "sort"

// explanations gives each consistency check a stable rationale + remediation, so
// a finding is never an opaque block — `nugit explain <check>` reads these.
var explanations = map[string]string{
	"c4<->code": "Go code introduced an import edge between two C4 components that workspace.dsl does not declare.\n" +
		"Fix: add the `src -> dst` relationship to the model, or remove the import.",
	"cmake<->code": "CMake target_link_libraries links two components the model does not declare.\n" +
		"Fix: add the `src -> dst` relationship to workspace.dsl, or remove the link.",
	"python<->code": "A Python import crosses two components the model does not declare.\n" +
		"Fix: add the `src -> dst` relationship to workspace.dsl, or remove the import.",
	"ts<->code": "A TypeScript/JS import crosses two components the model does not declare (resolved by dependency-cruiser).\n" +
		"Fix: add the `src -> dst` relationship, remove the import, or install dependency-cruiser to enforce TS edges.",
	"stale-knowledge": "The PR changes code governed by a superseded/invalidated knowledge object without updating it.\n" +
		"Fix: update the object, or supersede it with a new record.",
	"decision-coverage": "An architecturally-significant change has no accompanying decision record.\n" +
		"A `status: proposed` ADR is a candidate, not ratified knowledge, so it does not satisfy this check (ADR-0016).\n" +
		"Fix: add an ADR under .nugit/decisions/ (or a `decision:` commit trailer); ratify a proposed ADR with `nugit ratify <id>`.",
	"spec-linkage": "A commit references a spec id that has no matching object in-tree.\n" +
		"Fix: add the spec under .nugit/**/specs/, or correct the reference.",
	"capture-hygiene": "A commit trailer block is present but missing a mandatory field (learned:/keywords:).\n" +
		"Fix: add the field, or drop the trailer block.",
	"model-health": "workspace.dsl has a duplicate component id or an invalid path glob.\n" +
		"Fix: rename/merge the duplicate, or correct the glob.",
}

// Explain returns the rationale + remediation for a check id.
func Explain(check string) (string, bool) {
	s, ok := explanations[check]
	return s, ok
}

// AllChecks returns every known check id, sorted.
func AllChecks() []string {
	out := make([]string, 0, len(explanations))
	for k := range explanations {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
