---
schema_version: 1
id: LESSON-path-bound-counts-as-bound-but-never-enforced-a-bi
type: lesson
scope: evidence
status: active
created: 2026-08-04T10:03:47Z
provenance:
  commit: 5d28e7e2
confidence: medium
---

# Lesson — path-bound counts as bound but never enforced: a binding verified only by a warn…

**Trigger:** a knowledge object bound straight to a file path is checked only at warn severity, so counting it as fully enforced would overstate what the tool actually proves

**Insight:** path-bound counts as bound but never enforced: a binding verified only by a warn check must not claim the fail-severity tier

**Rejected:** pseudo-components for infra files — fake C4 elements with no edges pollute the diagram, delta, and c4<->code check

**Keywords:** applies_to_paths, retrieval, stale-knowledge, evidence tiers, doublestar, path binding
