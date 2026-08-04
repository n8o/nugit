---
schema_version: 1
id: ADR-0030
type: decision
scope: gitutil
status: proposed
created: 2026-08-04T10:03:47Z
relates_to:
  - constrains:gitutil
provenance:
  commit: 67dbca0b
confidence: medium
---

# ADR-0030 — staged-diff reads on the hook path take an explicit timeout; errors surface for …

## Context

feat(gitutil): bounded staged-diff numstat for hook-path callers

## Decision

staged-diff reads on the hook path take an explicit timeout; errors surface for the caller to degrade on

## Rejected

reusing Numstat with a pseudo-ref — --cached is a distinct plumbing mode and the hook needs the time bound

## Consequences

Promoted from commit `67dbca0b` by `nugit distill`.
