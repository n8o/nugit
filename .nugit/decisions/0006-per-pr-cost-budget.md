---
schema_version: 1
id: ADR-0006
type: decision
scope: global
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:engine
  - constrains:significance
provenance:
  commit: bootstrap
confidence: medium
---

# ADR-0006 — Per-PR compute budget; deterministic by default

## Context

The review noted the PR renderer was uncosted: recomputing four deltas +
embeddings + three-linter validation + an LLM narrative on every PR open and sync
is the most probable reason a team disables the check at monorepo scale.

## Decision

1. **The keystone is pure-deterministic and LLM-free.** All four deltas, the
   significance heuristic, and the consistency checks are set/graph operations
   over committed text. Wall-clock is dominated by `git diff` + parsing a handful
   of changed Go files — milliseconds, no network, no API spend.
2. **The LLM prose narrative is opt-in and gated** — it runs only on the
   architectural tier (§9.4) and is cached by a hash of the computed deltas, so an
   unchanged PR re-sync costs nothing.
3. **Fitness-function validation runs only on changed components**, not the whole
   graph, and only once a linter backend is adopted (ADR-0004).

## Consequences

- The default check-run is fast and free; cost scales with architectural change,
  not PR count.
- A per-run time budget can fail open (render the deterministic view, skip prose)
  rather than block the check.

## Rejected

- **LLM narrative on every PR sync (spec's implied default).** Rejected: cost and
  latency that grows with PR volume, with no caching — the disable-the-check
  failure mode.
- **Full-graph validation on every PR.** Rejected: redundant work; only changed
  components can introduce drift.
