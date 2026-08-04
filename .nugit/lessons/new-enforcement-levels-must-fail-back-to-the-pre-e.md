---
schema_version: 1
id: LESSON-new-enforcement-levels-must-fail-back-to-the-pre-e
type: lesson
scope: config
status: active
created: 2026-08-04T10:03:47Z
provenance:
  commit: 04ddb3bf
confidence: medium
---

# Lesson — new enforcement levels must fail back to the pre-existing default, not to the ne…

**Trigger:** a mistyped level in a config enum could quietly select stricter behavior than the author intended, with nothing in the output naming the value that was actually read

**Insight:** new enforcement levels must fail back to the pre-existing default, not to the new level — config typos must never opt a team into behavior they did not choose

**Keywords:** config, capture, commit_msg, nudge
