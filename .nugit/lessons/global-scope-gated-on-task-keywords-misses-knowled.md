---
schema_version: 1
id: LESSON-global-scope-gated-on-task-keywords-misses-knowled
type: lesson
scope: retrieval
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: 6ca44e05
confidence: medium
---

# Lesson — global scope gated on task keywords misses knowledge that is about a specific fi…

**Trigger:** 189 of 600 commits touched only infra paths the model maps to nothing, and 4 of 21 logged retrievals resolved to no component — the decision governing the changed file existed but never surfaced

**Insight:** global scope gated on task keywords misses knowledge that is about a specific file; a declared path binding is deterministic where keyword match is opportunistic

**Keywords:** applies_to_paths, retrieval, scope, path binding, infra, stale-knowledge
