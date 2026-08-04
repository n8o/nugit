---
schema_version: 1
id: ADR-0031
type: decision
scope: distill
status: accepted
created: 2026-08-04T10:03:47Z
relates_to:
  - constrains:distill
provenance:
  commit: 309b6671
confidence: medium
---

# ADR-0031 — join a block's continuation lines before scoring it, refuse a component-label + …

## Context

fix(distill): score whole observation units, refuse fragments and changelog bullets

## Decision

join a block's continuation lines before scoring it, refuse a component-label + change-verb head, and never emit a candidate that ends mid-clause

## Rejected

widening the refusal to every labelled bullet — a real observation carries a component label too ("<pkg>: 4 of 21 lookups returned nothing"), and the label is not the tell; what follows it is

## Consequences

Promoted from commit `309b6671` by `nugit distill`.
