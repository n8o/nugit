---
schema_version: 1
id: ADR-0009
type: decision
scope: bootstrap
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:gitutil
  - constrains:bootstrap
  - constrains:engine
  - prevents:ADR-0008
provenance:
  commit: bootstrap
  citation: internal/gitutil/gitutil.go
confidence: high
---

# ADR-0009 — Git-root-relative coordinates + language-agnostic structural mode

## Context

nugit silently assumed the nugit root (`.nugit/`) was the git root, and that the
codebase was Go. Both break on real repos: a polyglot monorepo (C++/Python/TS)
with a Go module nested at `apps/op/` (no root `go.mod`) couldn't be adopted at
all — model globs were nugit-root-relative while git's diff/show paths are
git-root-relative, so every path orphaned, and the Go-only bootstrap ignored the
non-Go bulk.

## Decision

1. **One coordinate system: git-root-relative.** `gitutil.Repo.Prefix()`
   (`rev-parse --show-prefix`) is the single bridge. Model `paths` globs are
   written git-root-relative (carrying the prefix, e.g. `apps/op/pkg/**`), so they
   match git's diff paths directly; the prefix also locates `.nugit/config.yml`,
   `workspace.dsl`, and `go.mod` for `ShowFile`, and bridges module-relative
   import dirs in the c4↔code check. **When prefix=="" everything is byte-identical
   to before** — no regression for the common case.
2. **Structural bootstrap.** When no Go module is rooted at the dir, `nugit init`
   discovers components from the **directory layout** (one per `apps/*`, `libs/*`,
   … container child, or per top-level dir), emitting components + globs with
   **no edges**. Any codebase gets a usable model + the full language-neutral PR
   view. `-layout container|toplevel|flat` controls granularity.
3. **Honesty rule.** A structural model carries an explicit note in `workspace.dsl`
   that relationships were not derived; `c4<->code` stays silent (no Go module), so
   a clean run is *not* an architecture guarantee. nugit never reports a
   falsely-green un-analyzable edge.

## Consequences

- Monorepos and nested modules work; JBS yields a 71-component structural model.
- Per-language analyzers (P2) later add edges + enforcement on top of the same
  git-root-relative globs.

## Rejected

- **Strip the prefix off every diff path at resolve time.** Rejected: it would
  touch every reader (mapper, knowledge, checks); writing globs git-root-relative
  localizes the bridge to the bootstrap writer (one place).
- **Use the Go import graph whenever any Go file exists.** Rejected: a polyglot
  repo with incidental/vendored Go would get a misleading Go-only model. Gate the
  Go path on an actual root `go.mod`; otherwise go structural.
