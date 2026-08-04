---
schema_version: 1
id: LESSON-for-a-derived-eval-corpus-precision-beats-recall-a
type: lesson
scope: skillopt
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: a8b0ad9e
confidence: medium
---

# Lesson — for a derived eval corpus, precision beats recall — a skipped case

**Trigger:** an eval case whose input reveals its own answer still scores as a pass, so a corpus derived from lesson text silently inflates every measurement taken afterwards

**Insight:** for a derived eval corpus, precision beats recall — a skipped case

**Rejected:** emit every lesson unfiltered — a leaky case silently inflates the

**Keywords:** skillopt, export, eval, leakage, jsonl, knowledge, outbound
