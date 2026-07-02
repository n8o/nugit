---
schema_version: 1
id: ADR-0015
type: decision
scope: global
status: proposed
created: 2026-07-02T00:00:00Z
relates_to:
  - elaborates:ADR-0003
  - constrains:knowledge
  - constrains:retrieval
provenance:
  commit: seed
  citation: JBS ADR-JBS-0006 supersedes ADR-JBS-0003 §3 (pilot, 2026-07-02)
confidence: medium
---

# ADR-0015 — Partial supersession via an `amends:` edge (proposed)

## Context

Supersession is whole-object: `supersedes: <id>` derives the entire target to
`superseded` at read time (ADR-0003). Real decisions age in pieces. The JBS
pilot produced the concrete case: ADR-JBS-0006 (one MXL domain per node)
overturns **only §3** of ADR-JBS-0003 (domain-UUID mapping) while the rest of
0003 (BCP-007-03 conformance surface) remains the live constraint. Today the
author must pick between two wrong outcomes:

- **Declare `supersedes:`** — the whole target derives superseded, so retrieval
  *suppresses its still-valid guidance* as dead context (JBS's current state:
  0003's conformance rules no longer surface to agents).
- **Don't declare it** — the target surfaces fully accepted with no hint that
  part of it is overturned, and agents follow the dead part.

## Decision (proposed)

1. **New relation `amends:<id>`** in `relates_to` — no schema change; it is an
   edge like `constrains:`/`informs:`. Semantics: "this decision overrides part
   of the target; the rest of the target stands."
2. **Derived, not mutated** (ADR-0003 preserved): the amended target keeps its
   own status, but knowledge loading computes an `AmendedBy: [ids]` marker, and
   retrieval/pr-render annotate it — e.g. `ADR-JBS-0003 (accepted, amended by
   ADR-JBS-0006)` — so both objects surface together and the reader resolves
   precedence by recency, exactly as case law does.
3. **No section anchors.** `amends:ADR-0003#3` was considered and dropped:
   heading numbers are not stable identifiers, and the annotation's job is to
   force the newer document into view, not to machine-resolve which paragraph
   died. The amending ADR's Context must say in prose what it overrides.
4. **Convention, enforced socially not mechanically: one decision per ADR.**
   Composites like JBS-0003 (transport conformance + domain mapping + filtering
   in one object) are what make partial supersession necessary; reviewers
   should split them at authoring time so future supersession can be total.
5. JBS remediation once ratified: change ADR-JBS-0006 `supersedes: ADR-JBS-0003`
   → `relates_to: [amends:ADR-JBS-0003]`, restoring 0003's live guidance.

## Rejected

- **Fragment supersession (`supersedes: <id>#<section>`)** — anchor fragility
  (renumber a heading, silently orphan the edge), parser + effective-status
  complexity, and a false promise of machine-resolvable precedence.
- **Amendments log appended to the old ADR** — mutates a record ADR-0003 treats
  as immutable; recreates the same-file concurrent-write conflicts the
  supersede graph exists to avoid.
- **Full supersession + a restating ADR** (supersede 0003 entirely; write a new
  ADR restating the surviving 90%) — works, but duplicates ratified text at
  every partial change and breaks inbound references to the original id.
- **Status quo** — forces the lose-lose above; the JBS store is already paying
  it.

## Consequences

- Loader: compute `AmendedBy` from reverse `amends:` edges (mirrors the
  supersedes pass). Retrieval + render: annotate. Small, additive.
- `stale-knowledge` check gains a sharper trigger: touching code governed by an
  *amended* object should point at both the object and its amendment.
- Until ratified and implemented, nothing changes at read time — this document
  is the proposal.
