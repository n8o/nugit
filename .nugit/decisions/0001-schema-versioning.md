---
schema_version: 1
id: ADR-0001
type: decision
scope: global
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:model
  - constrains:knowledge
provenance:
  commit: bootstrap
  citation: internal/model/model.go
confidence: high
---

# ADR-0001 — Stable cross-reference keys + schema versioning

## Context

The architecture spec derives a knowledge object's `id` from a content hash
(`base32(sha256(type+body))[:16]`). A review finding showed this makes the
on-disk format un-upgradeable: any change to the field set, the canonicalization,
or the hash algorithm re-hashes every object and **breaks every `relates_to` /
`supersedes` edge**, because edges are keyed by the hash. For a store meant to
preserve "what did the agent know at commit X" across years, that is fatal.

## Decision

1. **Cross-references use a stable, human-assigned `key`** (`ADR-0001`,
   `SPEC-001`, `LESSON-<slug>`), never a content hash. Edges point at keys, so
   re-hashing a body never breaks the graph.
2. **Content-addressing is deferred** to the index epic. When introduced, the
   hash becomes an *additional* integrity field, not the cross-reference key.
3. Every object carries `schema_version`. A `nugit migrate` step (future) maps
   old versions forward; the reader tolerates a missing/older version.
4. `canonical_body` (when content-addressing lands) will be defined precisely
   with a published test vector before any hash is persisted.

## Consequences

- The graph is stable under schema evolution and body edits.
- Keystone uses readable keys today; nothing about retrieval depends on hashes.
- A migration path exists by construction instead of being bolted on later.

## Rejected

- **Content-hash as the cross-reference key (spec's original).** Rejected: it
  couples identity to byte-level content, so the store cannot be migrated without
  rewriting every edge — exactly the un-upgradeable-format failure the review
  flagged. Keys decouple identity from content.
- **No version field, "we'll figure it out later".** Rejected: format changes are
  inevitable; without a version field the reader cannot tell v1 from v2 and
  migration becomes guesswork.
