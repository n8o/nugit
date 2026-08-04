---
schema_version: 1
id: LESSON-ratification-is-a-reviewable-store-mutation-not-a
type: lesson
scope: global
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: 8a4bd72d
confidence: medium
---

# Lesson — ratification is a reviewable store mutation, not a side effect of merging code —…

**Trigger:** eleven decisions had their code merged to the trunk while every record still sat at status proposed, so the store described shipped behavior as an open candidate

**Insight:** ratification is a reviewable store mutation, not a side effect of merging code — the candidate lane only pays off if promoting out of it is its own auditable step

**Keywords:** ratify, candidate lane, ADR-0016, lifecycle, store health, proposed, accepted
