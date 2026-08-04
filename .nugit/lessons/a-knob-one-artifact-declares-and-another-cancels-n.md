---
schema_version: 1
id: LESSON-a-knob-one-artifact-declares-and-another-cancels-n
type: lesson
scope: render
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: 821368f0
confidence: medium
---

# Lesson — a knob one artifact declares and another cancels needs a surface that reports th…

**Trigger:** config declared enforce mode and a failing gate, yet undeclared containers kept landing green because the CI invocation passed a weaker flag and nothing reported the disagreement

**Insight:** a knob one artifact declares and another cancels needs a surface that reports the disagreement, or the downgrade becomes immortal

**Rejected:** make config always win over the flag — breaks legitimate local weaker runs and adoption ramps

**Keywords:** enforcement, fail-on, downgrade, visibility, pr-render
