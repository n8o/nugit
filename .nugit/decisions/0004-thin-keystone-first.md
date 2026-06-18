---
schema_version: 1
id: ADR-0004
type: decision
scope: global
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:engine
  - satisfies:SPEC-001
provenance:
  commit: bootstrap
confidence: high
---

# ADR-0004 — Ship the keystone first; defer infrastructure to its trigger

## Context

The original plan put the only demo-able win (the unified PR view) last, gated
behind ~13 subsystems (content-addressed store, FTS5 + vector index, merge
driver, distiller, three linter backends, MCP server). The review's strategic
finding: realistic failure mode is the build stalling in infra (M1 vectors, M3
three linters) before ever validating the thesis.

## Decision

Invert the sequence:

1. **Spike (done):** prove the file→component mapping + significance heuristic on
   nugit's own Go code.
2. **Keystone (done):** `nugit pr-render` over commit-trailers + in-tree markdown
   + a parsed `workspace.dsl` diff. No index, no content-addressing, no
   embeddings, no merge driver.
3. **Infrastructure is conditional**, each behind an explicit trigger:
   - a fitness-function backend when a *second* language joins the repo;
   - an FTS index + `context()` when grep over `.nugit/` gets slow;
   - content-addressing + the merge driver when concurrent-write pain is actually
     observed (not before).

## Consequences

- The thesis is demonstrable in the first milestone and can course-correct.
- Each heavy subsystem is justified by a real, observed need, not anticipated.
- The atomicity principle (knowledge committed with code) holds on plain files —
  it never required the index or the merge driver.

## Rejected

- **Build the substrate first, render last (spec's M0→M5 order).** Rejected:
  concentrates all value at the end and maximizes the chance of dying in infra
  before proving the idea.
- **Skip the C4 model in the keystone too.** Rejected: the consistency check
  (the actual novelty) needs component edges, so a parsed model is the minimum
  that still demonstrates *verification*, not just presentation.
