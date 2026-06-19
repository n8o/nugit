---
schema_version: 1
id: ADR-0010
type: decision
scope: global
status: accepted
created: 2026-06-19T11:37:21Z
relates_to:
  - constrains:doctor
  - constrains:cli
provenance:
  commit: d3912d39
confidence: medium
---

# ADR-0010 — a dedicated doctor pre-flight separate from pr-render

## Context

feat: nugit doctor — setup pre-flight health checks

## Decision

a dedicated doctor pre-flight separate from pr-render

## Rejected

fold the checks into pr-render (different question — "is setup ok" vs "is this PR ok")

## Consequences

Promoted from commit `d3912d39` by `nugit distill`.
