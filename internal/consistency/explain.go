package consistency

import "sort"

// explanations gives each consistency check a stable rationale + remediation, so
// a finding is never an opaque block — `nugit explain <check>` reads these.
var explanations = map[string]string{
	"c4<->code": "Go code introduced an import edge between two C4 components that workspace.dsl does not declare.\n" +
		"Fix: add the `src -> dst` relationship to the model, or remove the import.\n" +
		"In a leveled model the check spans both levels: a container edge `A -> B` also covers every cross-container\n" +
		"component dependency from A into B (roll-up), but intra-container pairs are only ever covered by a component-level edge.",
	"cmake<->code": "CMake target_link_libraries links two components the model does not declare.\n" +
		"Fix: add the `src -> dst` relationship to workspace.dsl, or remove the link.\n" +
		"In a leveled model a container edge `A -> B` also covers cross-container component links (roll-up); intra-container pairs need a component-level edge.",
	"python<->code": "A Python import crosses two components the model does not declare.\n" +
		"Fix: add the `src -> dst` relationship to workspace.dsl, or remove the import.\n" +
		"In a leveled model a container edge `A -> B` also covers cross-container component imports (roll-up); intra-container pairs need a component-level edge.",
	"ts<->code": "A TypeScript/JS import crosses two components the model does not declare (resolved by dependency-cruiser).\n" +
		"Fix: add the `src -> dst` relationship, remove the import, or install dependency-cruiser to enforce TS edges.\n" +
		"In a leveled model a container edge `A -> B` also covers cross-container component imports (roll-up); intra-container pairs need a component-level edge.",
	"stale-knowledge": "The PR changes code governed by a superseded/invalidated knowledge object without updating it.\n" +
		"The governed surface is reached via governed components (scope/constrains edges) or via a direct\n" +
		"`applies_to_paths` file binding (ADR-0020) — the latter needs no C4 component.\n" +
		"Fix: update the object, or supersede it with a new record.",
	"decision-coverage": "An architecturally-significant change has no accompanying decision record.\n" +
		"A `status: proposed` ADR is a candidate, not ratified knowledge, so it does not satisfy this check (ADR-0016).\n" +
		"Fix: add an ADR under .nugit/decisions/ (or a `decision:` commit trailer); ratify a proposed ADR with `nugit ratify <id>`.",
	"spec-linkage": "A commit references a spec id that has no matching object in-tree.\n" +
		"Fix: add the spec under .nugit/**/specs/, or correct the reference.",
	"capture-hygiene": "A commit trailer block is present but missing a mandatory field (learned:/keywords:).\n" +
		"Fix: add the field, or drop the trailer block.",
	"model-health": "workspace.dsl has a duplicate element id (components and containers share one namespace), an invalid\n" +
		"path glob, or a relationship endpoint that names no declared component or container — or a knowledge object\n" +
		"declares an invalid `applies_to_paths` glob (ADR-0020), so its file binding is dead.\n" +
		"Fix: rename/merge the duplicate, correct the glob, or fix/declare the endpoint.",
	"prose-supersession": "A knowledge object added/modified by this PR declares in prose (\"Supersedes <id>\") that it replaces a live\n" +
		"store object, but carries no front-matter `supersedes:`/`amends:` edge. Effective status is derived from edges\n" +
		"only (ADR-0003), so retrieval keeps serving both the new object and the contradicted one as live (ADR-0022).\n" +
		"Fix: add `supersedes: <id>` (whole-object) or `relates_to: [amends:<id>]` (partial, ADR-0015) to the superseding object.",
	"model-drift": "The PR touches a detected buildable/deployable unit (deploy artifact, CMake target dir, or Go package)\n" +
		"that no workspace.dsl element maps — model coverage decayed since the last refresh (ADR-0021).\n" +
		"Fix: run the nugit-model skill to refresh the model, or add a component/container stub with `properties { paths \"<dir>/**\" }`.\n" +
		"`nugit doctor` reports the full-repo unmodeled backlog; this check only warns about units the PR touches.",
	"recurrence": "A component this PR touches has accumulated several fix-typed commits (fix:/revert: subjects) inside the\n" +
		"configured window with no knowledge capture — the failure class is recurring and nothing durable was learned (ADR-0019).\n" +
		"Fix: capture the why (learned:/keywords: trailer + `nugit distill`, or an ADR/lesson), or — when governing knowledge\n" +
		"exists but proved too narrow — widen it with `nugit reinforce <id> -text \"…\" -keywords …`.\n" +
		"Knobs: recurrence.mode (warn|off), recurrence.window_days, recurrence.min_fixes in .nugit/config.yml.",
	"contract-obligation": "A ratified `contract` object — declared here or read from a peer store (ADR-0032) — names this repo as a\n" +
		"party and requires something of it that the reviewed ref does not satisfy (ADR-0033). This is the two-sided\n" +
		"invariant case: one repo writes down \"the sibling must add a mirror guard\", and until now nothing in the\n" +
		"sibling's CI could ever fail when its half was missing.\n" +
		"Fix: satisfy the obligation in this repo, or supersede the contract in the repo that declares it.\n" +
		"Assertions are DECLARATIVE only — a Go RE2 regexp (linear time) over one named file, optionally negated with\n" +
		"`absent: true`. A contract can never execute a script or a command: it is text read out of another repo's\n" +
		"checkout, so running it would hand that repo this repo's CI credentials. That boundary is permanent.\n" +
		"A `must` whose file does not resolve at the reviewed ref is UNMET, not an error — unverifiable is not met.\n" +
		"Knobs: org.repo (this repo's stable org-wide party id; absent ⇒ the check is inert and never guesses) and\n" +
		"contracts.mode (warn|fail|off, default warn) in .nugit/config.yml.",
}

// topics documents cross-cutting concepts that are not check ids (kept apart
// from explanations so AllChecks stays a list of checks).
var topics = map[string]string{
	"evidence-tiers": "Every knowledge object carries a derived trust tier — how much of it nugit mechanically verifies:\n" +
		"  enforced — every governed component is path-bound AND the c4<->code edge checks fail on violations (enforce mode, backend active).\n" +
		"  checked  — at least one governed component is path-bound (or the object binds files directly via applies_to_paths,\n" +
		"             ADR-0020 — capped here: a direct binding is verified only by the warn-severity stale-knowledge check),\n" +
		"             but the binding is not fully edge-enforced (warn mode, no backend, or structural model).\n" +
		"  declared — written down; no scope/constrains edge binds it to code.\n" +
		"  proposed — candidate lane (ADR-0016), awaiting `nugit ratify`.\n" +
		"  stale    — superseded or invalidated.\n" +
		"Honesty caveat: \"enforced\" claims the governance SUBSTRATE is verified — undeclared dependencies touching the object's\n" +
		"components fail the PR. It does not claim the object's prose constraint is itself proven.",
}

// Explain returns the rationale + remediation for a check id or topic.
func Explain(check string) (string, bool) {
	if s, ok := explanations[check]; ok {
		return s, ok
	}
	s, ok := topics[check]
	return s, ok
}

// AllTopics returns every documented topic id, sorted.
func AllTopics() []string {
	out := make([]string, 0, len(topics))
	for k := range topics {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
