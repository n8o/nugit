---
schema_version: 1
id: ADR-0002
type: decision
scope: global
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:c4
  - constrains:mapping
  - constrains:consistency
provenance:
  commit: bootstrap
  citation: internal/mapping/mapping.go
confidence: high
---

# ADR-0002 — File→component binding via `properties { paths }` globs

## Context

Three of four deltas and two of five consistency checks need a deterministic
function from an arbitrary changed file to a C4 component: code-delta grouping,
the C4↔code check, the drift gate, and `context()` resolution. The review found
this primitive **entirely unspecified** — and Structurizr DSL has no native
source-location field, so it cannot be inferred from the model as written. This
single gap gated the keystone and the fitness-function epics.

## Decision

Each C4 component declares one or more repo-relative globs in the generic
Structurizr `properties` block:

```
render = component "Renderer" {
    properties { paths "internal/render/**" }
}
```

`internal/mapping` resolves a path to the owning component by matching these
globs, with a **most-specific-glob-wins** rule (longest literal prefix) and a
deterministic tie-break. Resolution is **total**: every path maps to exactly one
component id or to `""` (orphan), never ambiguously. Orphans are surfaced, never
silently dropped.

## Consequences

- The keystone's code-delta grouping, C4↔code check, and significance heuristic
  all build on one primitive, defined once and referenced everywhere.
- The binding lives in the model file, so it versions with the architecture.
- Imported package directories resolve via the same globs (`ResolveDir`), which
  is what lets the C4↔code check map an import edge onto a component edge.

## Rejected

- **Infer component membership from package names / directory structure.**
  Rejected: C4 components are logical and need not be 1:1 with packages; implicit
  inference silently mis-assigns files and is unexplainable when it does.
- **A separate mapping file outside workspace.dsl.** Rejected: it would drift from
  the model it annotates; co-locating the binding in the component keeps them
  atomic in one diff.
- **Defer the binding and group code by directory.** Rejected: then the C4↔code
  check (the trust core) cannot be computed at all — it needs component edges,
  not directory edges.
