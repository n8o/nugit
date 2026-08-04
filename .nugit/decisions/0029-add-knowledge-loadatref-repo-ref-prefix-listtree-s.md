---
schema_version: 1
id: ADR-0029
type: decision
scope: global
status: proposed
created: 2026-08-04T10:03:47Z
relates_to:
  - constrains:knowledge
  - constrains:engine
  - constrains:consistency
provenance:
  commit: e2fd628f
confidence: medium
---

# ADR-0029 — add knowledge.LoadAtRef(repo, ref, prefix) (ListTree + ShowFile, the beads patte…

## Context

fix(engine): load knowledge at the reviewed ref, not the working tree

## Decision

add knowledge.LoadAtRef(repo, ref, prefix) (ListTree + ShowFile, the beads pattern) and use it in the engine; retrieval keeps reading the working tree — live context is about the checkout, pr-render is about base..head

## Rejected

an io/fs abstraction over git trees — more machinery than the two-plumbing-call pattern the repo already uses (beads, cmake); also rejected leaving a disk fallback, which would silently reintroduce the drift

## Consequences

Promoted from commit `e2fd628f` by `nugit distill`.
