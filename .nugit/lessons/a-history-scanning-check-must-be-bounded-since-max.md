---
schema_version: 1
id: LESSON-a-history-scanning-check-must-be-bounded-since-max
type: lesson
scope: consistency
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: 0b6d5aa1
confidence: medium
---

# Lesson — a history-scanning check must be bounded (since + max-count) and warn-only, beca…

**Trigger:** a check that walks commit history has no natural stopping point: on a long-lived repo it scans everything, and the evidence it finds decays the further back it reaches

**Insight:** a history-scanning check must be bounded (since + max-count) and warn-only, because its evidence lies outside the PR under review

**Keywords:** recurrence, fix-churn, consistency, git-log, warn
