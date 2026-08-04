---
schema_version: 1
id: LESSON-when-one-field-can-both-name-a-record-and-state-on
type: lesson
scope: distill
status: active
created: 2026-08-04T10:03:47Z
provenance:
  commit: d899aa79
confidence: medium
---

# Lesson — when one field can both name a record and state one, split the two forms before …

**Trigger:** distilling a 30-commit range promoted 14 decision records, 8 of them titled just `adr-0019` or `adr-0025`, each with a `## Decision` section whose whole body was a key like `ADR-0026`

**Insight:** when one field can both name a record and state one, split the two forms before anything is minted from it — otherwise the reference form silently duplicates the record it points at

**Rejected:** minting when a bare key is unknown to the store — a bare key carries no statement, so the record could only ever restate an id; a dangling reference is not a decision

**Keywords:** distill, citation, duplicate, trailers, proposal, adr
