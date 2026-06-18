---
schema_version: 1
id: LESSON-read-from-reviewed-ref
type: lesson
scope: consistency
status: active
created: 2026-06-18T00:00:00Z
relates_to:
  - prevents:ADR-0002
  - constrains:goimports
provenance:
  commit: bootstrap
  citation: internal/goimports/goimports.go
confidence: high
---

# Lesson — a PR-time analyzer must read every artifact from the reviewed ref

**Trigger:** computing a delta or check that compares "the code" against "the model"
inside a PR-time / CI tool.

**Insight:** read every artifact (source, model, knowledge) from the *reviewed git
ref*, never from the working tree. The keystone's import analyzer originally parsed
`.go` files off disk while the changed-file set came from `base..head`. Whenever the
working tree isn't byte-identical to head — a CI merge commit, local edits, a clean
checkout at base — the import graph silently disagrees with the diff it claims to
describe, producing both false negatives (missed undeclared edges) and false
positives. The fix: `git show <ref>:<path>` and feed the bytes to the parser.

**Rejected:** reading files from disk "because it's simpler" — it couples the analysis
to whatever happens to be checked out, which is exactly what a deterministic,
ref-addressed tool must not do.

**Keywords:** ci, determinism, git, working-tree, import-graph, false-positive
