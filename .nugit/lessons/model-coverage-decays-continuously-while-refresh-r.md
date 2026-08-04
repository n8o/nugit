---
schema_version: 1
id: LESSON-model-coverage-decays-continuously-while-refresh-r
type: lesson
scope: modelfacts
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: 38093205
confidence: medium
---

# Lesson — model coverage decays continuously while refresh rituals are punctual; diff dete…

**Trigger:** eleven real code units were missing from the architecture model, three of them visible to the build-graph detector before the last manual refresh and missed anyway

**Insight:** model coverage decays continuously while refresh rituals are punctual; diff detector facts against the DSL at the moment someone touches the drifted unit, and keep the backlog view in doctor

**Rejected:** full-scan warn on every PR — bills the whole modeling backlog to whoever touches the repo next; warns become wallpaper and get muted

**Keywords:** model-drift, consistency, modelfacts, doctor, coverage, detectors, ADR-0021
