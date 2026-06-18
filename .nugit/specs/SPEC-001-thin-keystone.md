---
schema_version: 1
id: SPEC-001
type: spec
scope: global
status: active
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:engine
  - constrains:render
provenance:
  commit: bootstrap
confidence: high
---

# SPEC-001 — Thin keystone: `nugit pr-render`

The unified PR view computed from a repo's own committed artifacts, with no
index, no content-addressing, no embeddings, and no merge driver.

## Requirements (EARS)

- **R1 (ubiquitous):** The system SHALL compute four deltas — C4, code, knowledge,
  plan — deterministically from `mergeBase(base, head)..head`.
- **R2 (event):** WHEN code introduces an import edge between two C4 components that
  `workspace.dsl` does not declare, the system SHALL emit a `c4<->code` finding at
  `fail` severity.
- **R3 (event):** WHEN a PR changes code governed by a `superseded`/`invalidated`
  knowledge object without updating it, the system SHALL emit a `stale-knowledge`
  warning.
- **R4 (state-driven):** WHILE a change is classified architectural, IF no decision
  record or `decision:` trailer accompanies it, the system SHALL emit a
  `decision-coverage` warning.
- **R5 (ubiquitous):** The system SHALL group the code delta by C4 component using the
  `properties { paths }` bindings, reporting files matching no component as `unmapped`.
- **R6 (ubiquitous):** The renderer SHALL collapse non-relevant sections for trivial
  changes and expand all sections for architectural changes (progressive disclosure).
- **R7 (ubiquitous):** The system SHALL emit markdown, a GitHub check-run conclusion,
  and a structured-delta JSON from the same computed report.

## Acceptance criteria

- AC1: On a clean PR (model matches code), `pr-render` reports zero `fail` findings.
- AC2: Introducing an undeclared cross-component import yields a `fail` finding naming
  the `src → dst` edge and the file that introduced it.
- AC3: A two-line single-component change renders as tier `trivial` with collapsed
  knowledge/architecture sections.
- AC4: `check-run` conclusion is `failure` iff any finding is `fail`, else `neutral`
  iff any `warn`, else `success`.

## Verification contract

- AC1–AC4 are exercised by `internal/engine` and package tests run in CI via
  `go test ./...`. The "criteria met" half of spec-linkage is NOT auto-claimed beyond
  these tests (honesty note — see [[0002-file-to-component-binding]]).
