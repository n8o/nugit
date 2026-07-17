---
schema_version: 1
id: ADR-0017
type: decision
scope: global
status: accepted
created: 2026-07-17T00:00:00Z
relates_to:
  - constrains:c4
  - constrains:mapping
  - constrains:consistency
provenance:
  commit: seed
confidence: high
---

# ADR-0017 — Two-level c4↔code enforcement with container roll-up

## Context

Containers were transparent scaffolding: the parser descended through a
`container` block and recorded only the components inside, so a
skill-conformant leveled model (services-as-containers, ADR-0012) was silently
unenforced. Container path bindings leaked into model-level properties where
nothing read them, container relationships (`A -> B`) dangled with endpoints
no check could resolve, and a repo that leveled its model per the nugit-model
skill got a green pr-render that guaranteed nothing about its cross-service
edges. The only reason flat repos were safe is that they never author
containers — nugit's own DSL wraps everything in a single paths-less `app`
container that behaves like whitespace.

## Decision

1. **Containers become first-class as a SEPARATE `Model.Containers` slice** —
   never entries in `Components`. Every existing consumer iterates Components
   and is byte-identical by construction; components record their parent
   container id.
2. **Enforcement spans both levels via `Covered(src, dst)` roll-up:**
   - same lineage (a container and its own child, or identity) is containment,
     never a dependency;
   - within one container, ONLY a literal component edge covers — a container
     edge NEVER covers intra-container pairs;
   - across containers (or mixed levels), any declared edge over
     {src, scope(src)} × {dst, scope(dst)} covers: component edge, container
     edge (roll-up), or mixed.
   Flat models reduce exactly to `HasRelationship` — no behavior change.
3. **Component-level edges are recommended first** in findings; the
   container-level alternative is offered as the coarser opt-in.
4. **The model stays literal.** No synthesized roll-up edges appear in the
   delta, the retrieval c4 slice, or the diagram — nugit renders only what the
   author declared.

## Rejected

- **Kind field in Components** — every consumer iterating Components would see
  wrapper containers; output drift in every repo whose DSL nests components in
  a container (including nugit itself).
- **Container edges cover only otherwise-unresolvable pairs** — makes a
  declared `A -> B` near-useless (it would stop covering the moment a child
  gets its own paths) and gives the worst authoring ergonomics: whether an edge
  counts depends on unrelated path bindings.
- **Synthesizing implied container edges into the model** — phantom deltas
  (edges appearing in diffs nobody authored) and non-authored facts rendered as
  if ratified; violates the model-stays-literal principle.

## Consequences

- One legitimate `A -> B` legalizes every future A.* → B.* crossing — the
  known roll-up hole. Defenses: the edge's introduction is itself visible and
  ratified (a container edge is an architectural C4Delta), intra-container
  pairs are never covered, and findings recommend the narrow component edge
  first. A `c4.container_rollup: off` knob is a possible future escape hatch.
- gen-rules rolls container edges DOWN to the flat cross-product (go-arch-lint
  has no container level); evidence tiers treat a container as path-bound via
  its own paths or any path-bound child; retrieval extends scope to the parent
  container.
- doctor and the IcePanel projection remain component-only — container
  awareness there is follow-up work.
