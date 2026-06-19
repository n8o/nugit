---
schema_version: 1
id: SPEC-002
type: spec
scope: global
status: active
created: 2026-06-19T00:00:00Z
relates_to:
  - elaborates:ADR-0011
  - constrains:delta
provenance:
  commit: integration-phase-0
confidence: high
---

# SPEC-002 — External tool integration contract

The normative behavior every integration adapter must satisfy (governed by
[[0011-external-tool-integration-single-writer]]).

## Outbound projection (git → tool)

- **WHEN** a push lands on the default branch, the projection job **SHALL** transform
  the git-authoritative artifact and overwrite tool state in full
  (IcePanel `import?prune=true`; Notion `replace_content`), so tool state is a pure
  function of the pushed ref.
- The job **SHALL** key every tool object to a stable git-owned id, updating in place;
  it **SHALL NOT** create duplicates on re-run (idempotent).
- It **SHALL** archive/prune tool objects whose git source was removed (no zombies).
- A tool edit that contradicts git **SHALL** be overwritten on the next push (tool
  writes are ephemeral by construction).

## Inbound proposal (tool → git)

- A tool-side edit **SHALL** surface only as a *reviewed PR* that edits the git text;
  no adapter writes `.nugit/**` directly.
- The harvester **SHALL** semantically diff tool state against the last published
  artifact and **SHALL NOT** re-propose nugit's own outbound writes (loop avoidance via
  a nugit-authored marker + content hash).

## Plan-position (contextual, the carve-out)

- A plan source (Beads file, Linear live) **MAY** be read at report time but
  **SHALL NOT** gate a PR, and **SHALL** carry a provenance `Note` marking it
  live/forecast.
- On a live source's error/rate-limit, the adapter **SHALL** degrade
  (Linear → Beads → `plan.yml`) and **SHALL NOT** fail the review.

## Acceptance

- Two consecutive outbound pushes of an unchanged model produce no tool-object churn.
- A removed git artifact archives its tool projection.
- A plan-source outage degrades without failing `pr-render`.
