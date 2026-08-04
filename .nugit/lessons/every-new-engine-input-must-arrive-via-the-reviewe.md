---
schema_version: 1
id: LESSON-every-new-engine-input-must-arrive-via-the-reviewe
type: lesson
scope: engine
status: active
created: 2026-08-04T10:03:47Z
provenance:
  commit: e2fd628f
confidence: medium
---

# Lesson — every new engine input must arrive via the reviewed ref; grep the pr-render path…

**Trigger:** When the checkout drifts from head (CI merge commit, local edits, deleted files) the stale-knowledge and spec-linkage checks describe the wrong corpus: suppressed findings for objects deleted on disk, fabricated ones for uncommitted files.

**Insight:** every new engine input must arrive via the reviewed ref; grep the pr-render path for RepoDir reads when adding one

**Rejected:** an io/fs abstraction over git trees — more machinery than the two-plumbing-call pattern the repo already uses (beads, cmake); also rejected leaving a disk fallback, which would silently reintroduce the drift

**Keywords:** determinism, working-tree, reviewed-ref, stale-knowledge, spec-linkage
