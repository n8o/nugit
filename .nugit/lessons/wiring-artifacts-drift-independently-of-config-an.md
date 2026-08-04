---
schema_version: 1
id: LESSON-wiring-artifacts-drift-independently-of-config-an
type: lesson
scope: doctor
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: 2dbf1499
confidence: medium
---

# Lesson — wiring artifacts drift independently of config; an advisory scan catches the dri…

**Trigger:** a skill file still told agents the model ran in warn mode weeks after config flipped to enforce, and three install pins disagreed across the agent docs and CI

**Insight:** wiring artifacts drift independently of config; an advisory scan catches the drift without making doctor flaky

**Rejected:** hard-fail doctor on wiring drift — a regex scan of prose is not confident enough to gate a pre-flight that must stay safe everywhere

**Keywords:** doctor, wiring, drift, fail-on, pins, skill
