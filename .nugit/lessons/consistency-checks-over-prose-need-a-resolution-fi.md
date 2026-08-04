---
schema_version: 1
id: LESSON-consistency-checks-over-prose-need-a-resolution-fi
type: lesson
scope: consistency
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: 7ef96e7e
confidence: medium
---

# Lesson — consistency checks over prose need a resolution filter (store ids) plus code-spa…

**Trigger:** a check reading supersession claims out of body prose fired on fenced code samples and on ids naming nothing in the store, so none of its findings could be trusted

**Insight:** consistency checks over prose need a resolution filter (store ids) plus code-span stripping to stay precise

**Rejected:** fail severity — prose matching has irreducible false positives; start at warn (ADR-0016 discipline)

**Keywords:** consistency, prose-supersession, warn, eval, ADR-0022
