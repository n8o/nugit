---
schema_version: 1
id: LESSON-a-composite-action-bundling-a-go-tool-must-build-f
type: lesson
scope: global
applies_to_paths:
  - "action.yml"
status: proposed
created: 2026-08-04T10:03:47Z
provenance:
  commit: 6ed0b50c
confidence: medium
---

# Lesson — a composite action bundling a Go tool must build from $GITHUB_ACTION_PATH, becau…

**Trigger:** the CI job died with "cannot find main module" in every consuming repo, while the same action passed in the repo that owns the tool

**Insight:** a composite action bundling a Go tool must build from $GITHUB_ACTION_PATH, because go build resolves package paths against the current directory's module, not the path argument's

**Keywords:** action, composite, go build, GITHUB_ACTION_PATH, module resolution, ci
