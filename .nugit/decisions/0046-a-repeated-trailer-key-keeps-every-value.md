---
schema_version: 1
id: ADR-0046
type: decision
scope: global
status: proposed
created: 2026-10-16T00:00:00Z
relates_to:
  - constrains:trailers
  - constrains:distill
  - informs:ADR-0005
provenance:
  commit: seed
  citation: "fix/repeated-trailers; two commits in this repo's own history carry two decision: lines, and distill minted only the second"
confidence: medium
---

# ADR-0046 — A repeated trailer key keeps every value

## Context

`trailers.Parse` assigned each recognized key with `=`, so when a key repeated
the last line won. Two commits in this repo already carry two `decision:`
lines (a commit that genuinely makes two decisions), and a squash-merge body
can carry several commits' blocks. In each case `nugit distill` minted one
decision and silently dropped the rest; repeated `rejected:` lines kept only
the last alternative, and repeated `affects:`/`keywords:` kept only the last
list. Nothing warned. The capture convention never said "one per commit", and
an agent writing a trailer block has no reason to guess it.

## Decision

A repeated key keeps every value:

- `decision:` — every value lands in `Trailer.Decisions`, in order, and
  `Trailer.Decision` is their `"; "` join, so callers that only ask "was a
  decision recorded?" or display it are unchanged. `distill` reads
  `AllDecisions()` and proposes each decision as its own record.
- `rejected:`, `learned:`, `symptom:` and unrecognized keys — values join with
  `"; "`; an exact repeat collapses.
- `affects:`, `keywords:` — ordered union.
- `spec:` — an id, not prose, so the first wins.

`Decisions` is `json:"-"`: the structured output keeps its existing shape and
the flat-model golden stays byte-identical.

## Rejected

- **Warn on a repeated key and keep last** — it blames the writer for a shape
  the convention never forbade, and squash merges produce it with no writer.
- **One joined decision record** — two decisions are two records; joining them
  makes one ADR that cannot be superseded half at a time.
- **Change `Decision` to a slice** — breaks every caller and the JSON contract
  for a case most commits never hit.

## Consequences

Re-running distill over an old range can now mint a decision it previously
dropped; dedupe against the store still applies. A joined `rejected:` is
attached to every decision of that commit, since a trailer cannot say which
alternative belonged to which decision.
