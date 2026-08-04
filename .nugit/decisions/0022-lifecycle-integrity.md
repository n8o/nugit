---
schema_version: 1
id: ADR-0022
type: decision
scope: global
status: proposed
created: 2026-08-01T00:00:00Z
relates_to:
  - constrains:knowledge
  - constrains:doctor
  - constrains:consistency
  - informs:ADR-0003
  - informs:ADR-0015
provenance:
  commit: seed
  citation: "feat/lifecycle-integrity"
confidence: high
---

# ADR-0022 — Store lifecycle integrity checks

## Context

Six weeks into the pilot, the store is drifting in ways no existing check
names. Four concrete findings, all from the live pilot store:

1. **Prose-only supersession.** A protocol-draft reference
   (REF-P-transport-14) is still `status: active` while the newer pilot ADR
   (ADR-P-0020, covering draft-16) says in its *body* "Supersedes
   REF-P-transport-14 … that is now stale." The supersession lives only in
   prose — no front-matter `supersedes:` edge — and effective status is
   derived exclusively from edges (ADR-0003), so retrieval serves BOTH the
   draft-16 decision and the contradicting draft-14 reference as live
   guidance. The store's headline invariant (dead context never surfaces as
   live) fails silently.
2. **Supersedes-as-list silent untype.** A pilot commit trailer (3653d9d2)
   filed the exact bug upstream: "nugit `supersedes:` is a single string, not
   a YAML list — a list silently downgrades the whole object to untyped and
   it vanishes from retrieval; worth a doctor check upstream." Today doctor
   lumps this under the generic untyped message; the author gets a −15 health
   deduction and no hint which field to fix.
3. **Provenance drift.** `provenance.commit` values in the wild: literal
   `HEAD` (a moving ref — meaningless as provenance), `seed` on objects that
   are not seeds, plus an undocumented `issues:` array inside `provenance`
   that the schema parser drops without a sound.
4. **Stuck candidates.** Five shipped ADRs are still `status: proposed` —
   their cited PRs merged weeks ago. The doctor pending line (ADR-0016) counts
   them but hides how long they have been stuck, so the lane reads as healthy
   churn instead of six weeks of unratified drift.

## Decision

Four small, precise lifecycle checks. Advisory/warn throughout — lifecycle
drift is real but never blocks a pre-flight or a PR (the ADR-0016 discipline:
start at warn, promote once the corpus proves the precision).

1. **Prose-supersession check** (doctor advisory + consistency warn,
   `prose-supersession`). A live typed object whose body matches a
   "Supersedes \<ID\>" pattern, where \<ID\> resolves to another live
   store object, and the declaring object carries neither `supersedes: <ID>`
   nor `relates_to: [amends:<ID>]`, warns: *supersession declared in prose
   only — add the front-matter edge so EffectiveStatus updates.* Precision
   guards: fenced code blocks and inline code spans never match (schema
   documentation quoting `supersedes: <id>` is not a declaration), the target
   must resolve, and an already-superseded/invalidated target never warns
   (the edge exists elsewhere; the drift is resolved). The consistency check
   fires only for objects the PR adds or modifies — pre-existing store drift
   is doctor's job, not every PR's. The shared core lives in
   `internal/knowledge` (`ProseOnlySupersessions`) so the two surfaces cannot
   drift apart.
2. **Supersedes-as-list targeted diagnosis** (doctor). When front-matter
   fails the typed schema, doctor re-parses it generically and detects the
   specific case of a scalar schema field (`supersedes:` et al.) authored as
   a YAML list, then names the field in both the check detail and the health
   reason — instead of the generic "untyped front-matter" message. The
   deduction magnitude is unchanged; what changes is that the author is told
   exactly which field to fix and why the object vanished.
3. **Provenance sanity** (doctor, advisory). `provenance.commit` should be a
   commit sha, a documented sentinel (`seed`, `bootstrap`), or absent.
   Flagged: literal `HEAD` (any case), an explicitly empty `commit:` key, and
   unknown keys inside `provenance:` (schema keys: `commit`, `agent`,
   `citation`) — those are data the parser silently drops, like the pilot's
   `issues:` array.
4. **Stale-proposed sharpening** (doctor, advisory). The pending-ratification
   line gains each object's age in days derived from `created:` —
   "ADR-X (44d)" — so a stuck lane is visible at a glance. It stays
   informational, exactly as ADR-0016 ratified.

## Rejected

- **Deriving the edge from prose automatically** (treat a prose "Supersedes
  X" as a real supersession) — retrieval semantics from unstructured text: a
  false match would silently derive a live object superseded, the precise
  failure mode ADR-0003 built the edge graph to avoid. The warn keeps a human
  in the loop; the fix is a one-line front-matter edit.
- **Failing (not warning) on prose-supersession** — pattern matching over
  prose has irreducible false positives (quoted examples, discussion of the
  mechanism itself). Warn + `nugit explain prose-supersession` is the
  established on-ramp; promote later if the corpus shows precision.
- **Widening the schema to accept `supersedes:` lists** — multi-target
  supersession is either consolidation (N single-supersede records express it
  today) or partial supersession (ADR-0015's `amends:` exists for that); a
  list type also bumps the frozen schema (ADR-0001) to legalize what is, in
  every observed case, an authoring mistake.
- **Verifying `provenance.commit` resolves in the clone** — squash-merge
  legitimately orphans feature-branch shas (ADR-0005), so honest provenance
  would flag; resolvability is a property of the clone, not the record.
  Validation stays syntactic.
- **Flagging slug commits (`bootstrap`, `ai-bootstrap-design`, …)** — the
  historic bootstrap idiom, present in nugit's own store; advisory noise with
  no wrong data behind it. Only provably-wrong values (`HEAD`, empty) flag.
- **Aging proposed objects into auto-expiry or a gating check** — ADR-0016
  deliberately made the pending line informational; age is disclosure, not a
  deadline.

## Consequences

- `internal/knowledge` gains `ProseOnlySupersessions` and a generic
  `RawFrontMatter` inspector; doctor and consistency consume the same core.
- `nugit doctor` gains two advisory checks (supersession-edges-match-prose,
  provenance-is-sane), a targeted untyped diagnosis naming the offending
  field, and ages on the pending line. None affect the exit code.
- `nugit pr-render` gains the `prose-supersession` warn for PR-touched
  objects, with an `explain` entry; the eval corpus and adversarial set gain
  a positive/negative pair for it.
- Pilot remediation is now mechanical: doctor names the prose-only
  supersession (add the edge; the draft-14 reference derives superseded), the
  list-valued `supersedes:`, the `HEAD`/`issues:` provenance, and the five
  stuck candidates with their ages.
