---
schema_version: 1
id: LESSON-any-git-call-added-to-the-commit-path-must-carry-i
type: lesson
scope: gitutil
status: active
created: 2026-08-04T10:03:47Z
provenance:
  commit: 67dbca0b
confidence: medium
---

# Lesson — any git call added to the commit path must carry its own deadline; the hook's ne…

**Trigger:** a git call on the commit path can block on a slow filesystem or a huge staged diff, and the hook has no way to abandon it — the commit stalls with nothing printed

**Insight:** any git call added to the commit path must carry its own deadline; the hook's never-block discipline is only as strong as its slowest subprocess

**Rejected:** reusing Numstat with a pseudo-ref — --cached is a distinct plumbing mode and the hook needs the time bound

**Keywords:** gitutil, numstat, staged, hook, timeout
