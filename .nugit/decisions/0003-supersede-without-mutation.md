---
schema_version: 1
id: ADR-0003
type: decision
scope: global
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:knowledge
  - prevents:ADR-0001
provenance:
  commit: bootstrap
  citation: internal/knowledge/knowledge.go
confidence: high
---

# ADR-0003 — Supersede without mutation; status is derived, not flipped

## Context

The spec simultaneously requires "one immutable record per file", "supersede,
don't edit", and "flip the old record's `status`". The review showed these are
mutually contradictory: flipping status mutates a file whose identity is its
content, and two branches superseding the same record edit the same file and
conflict — defeating the conflict-free guarantee.

## Decision

A record file is **append-only and never mutated after creation**. A correction
is a *new* record whose front-matter names `supersedes: <old-key>`. The
**effective status** of every record is computed at read time from the supersedes
graph: a record is `superseded` iff some other record supersedes it. The
`status:` field in front-matter is only the record's authored *initial* state.

## Consequences

- Records are genuinely immutable, so they can be content-hashed later (ADR-0001)
  and never produce same-file merge conflicts.
- Concurrent supersedes of the same record are two disjoint new files — they
  merge cleanly with zero conflict, preserving the headline property.
- `internal/knowledge.ResolveEffectiveStatus` is the single place this is
  computed; the renderer and checks consume the derived value.

## Rejected

- **Flip `status` in place (spec's original).** Rejected: it mutates an immutable,
  content-addressed file and creates same-file write conflicts under concurrency —
  the exact contradiction the review flagged.
- **A mutable per-record status sidecar.** Rejected: still a shared mutable write
  target; derivation from the append-only graph needs no extra file at all.
