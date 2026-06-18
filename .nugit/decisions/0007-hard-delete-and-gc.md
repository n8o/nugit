---
schema_version: 1
id: ADR-0007
type: decision
scope: global
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:knowledge
  - prevents:ADR-0003
provenance:
  commit: bootstrap
confidence: medium
---

# ADR-0007 — Hard-delete / erasure path despite immutable-in-git

## Context

"Immutable, ever-growing, in-git knowledge" conflicts directly with secret-leak
removal, PII erasure (right-to-erasure), and legal takedown — which require
*hard* deletion, not supersession. The review flagged this structural conflict as
unaddressed, and noted the store only ever grows (sharded files in every clone).

## Decision

1. **Supersede is the default; hard-delete is an explicit, audited escape hatch.**
   `nugit forget <key> --reason <r>` removes the record file and records a
   tombstone (key + reason + date, *no content*) so the graph stays consistent and
   the removal is itself auditable.
2. **True erasure (secrets/PII/legal) is a documented `git filter-repo` runbook**,
   accepted as a history-rewrite event. Because cross-references use stable keys
   (ADR-0001) and not content hashes, rewriting a blob does **not** break the
   knowledge graph — only the tombstone notes the erasure.
3. **`nugit compact` (future) garbage-collects** long-superseded records into a
   cold archive ref to bound working-tree growth, leaving tombstones behind.

## Consequences

- Legal/secret removal is possible without breaking the graph (keys, not hashes).
- Growth is bounded by an explicit GC step rather than assumed-away.
- Every removal leaves an auditable tombstone — deletion is not silent.

## Rejected

- **Immutable forever, never delete (spec's implication).** Rejected: violates
  erasure obligations and lets the store grow without bound.
- **Soft-delete only (mark invalidated).** Rejected: the content still exists in
  git history, which does not satisfy secret/PII removal.
