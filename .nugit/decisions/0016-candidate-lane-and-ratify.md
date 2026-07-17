---
schema_version: 1
id: ADR-0016
type: decision
scope: global
status: accepted
created: 2026-07-16T00:00:00Z
relates_to:
  - amends:ADR-0003
  - constrains:distill
  - constrains:consistency
  - constrains:retrieval
provenance:
  commit: seed
  citation: prior-art review, 2026-07-16 — candidate lanes never mint evidence tiers
confidence: high
---

# ADR-0016 — Candidate lane: distill mints `proposed`, `nugit ratify` promotes

## Context

`status: proposed` existed in the schema but nothing consumed it. `nugit
distill` minted ADRs as `accepted` and lessons as `active` at the moment of
promotion, making a machine-drafted record indistinguishable from a
deliberated, human-ratified one — the store could not tell its own candidates
from its corpus. A prior-art review surfaced the discipline worth copying:
AI-derived content lives in a structurally separate candidate lane and can
never mint trust on its own; only a deliberate act promotes it.

## Decision

1. **Distill mints `status: proposed`** (both ADRs and lessons). Escape hatch:
   `nugit distill -status ratified` restores the pre-lane behavior for teams
   that don't want the lane.
2. **Proposed does not satisfy `decision-coverage`.** A PR whose only decision
   evidence is a proposed ADR still warns, with a detail pointing at
   `nugit ratify`. The `decision:` trailer bypass stays — the trailer is the
   ADR-0005 capture primitive; the check verifies the why was *recorded*, and
   ratification is a separate, later act.
3. **Proposed stays in retrieval, labeled — never hidden.** Superseded /
   invalidated are known-wrong; proposed is merely unratified and often the
   only recorded rationale for fresh work. Hiding it would break the capture
   loop distill exists for. It renders as `proposed — unratified`, ranks below
   accepted/active within a scope band, and is truncated first. Scope-primary
   ordering is retained: a component-scoped proposed item may outrank a global
   accepted one, because nearest-scope-wins is the retrieval ethos.
4. **`nugit ratify <id>` is the single permitted in-place mutation** of a
   knowledge object: one line, `status: proposed` → `status: accepted`
   (decision/spec) or `status: active` (lesson/reference). **This clause
   amends ADR-0003**: the immutability contract attaches *at ratification* —
   a proposed record is a candidate, not yet a corpus member. The
   conflict-free property ADR-0003 protects survives: concurrent ratifies of
   the same record produce byte-identical edits and merge cleanly, and the
   edit lands only via a reviewed PR (ADR-0011).

## Rejected

- **Supersede-to-ratify** (write a new accepted object superseding the
  proposed one) — leaves a dead `proposed` file per promotion, and
  `superseded` semantically means "wrong", not "promoted"; it would poison
  stale-knowledge and the retrieval guards.
- **Excluding proposed from retrieval until ratified** — breaks the capture
  loop; the freshest why would be invisible exactly when agents need it.
- **Removing the trailer coverage bypass** (forcing a ratified ADR per
  architectural PR) — trailers are the deliberate capture act ADR-0005 builds
  on; distill + ratify is the promotion pipeline, not a gate on capture.
- **YAML re-marshal in ratify** — rewrites the whole file (key order, quoting,
  comments), destroying the one-line-diff property that makes ratification
  reviewable and merge-safe.

## Consequences

- Distilled output is second-class until a human ratifies it; the store's
  trust boundary becomes visible instead of implicit.
- `nugit doctor` reports proposed objects pending ratification (informational,
  never a pre-flight failure).
- The evidence-tier work builds on this: proposed maps to its own tier below
  declared, and ratification is what admits an object to enforced/checked.
- Existing stores are unaffected: already-minted `accepted`/`active` objects
  keep their status; only new distill runs change behavior.
