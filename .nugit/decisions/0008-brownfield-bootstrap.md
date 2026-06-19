---
schema_version: 1
id: ADR-0008
type: decision
scope: bootstrap
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:bootstrap
  - constrains:scaffold
  - constrains:config
  - prevents:ADR-0004
provenance:
  commit: bootstrap
  citation: internal/bootstrap/bootstrap.go
confidence: high
---

# ADR-0008 — Bootstrap the C4 model from the import graph, default to warn

## Context

`nugit pr-render` only adds value where a `workspace.dsl` with `paths` bindings
already exists. The review's highest-severity adoption gap: on a brownfield repo
the first run would either find no model (nothing to check) or — if someone
hand-wrote a partial model — fail the c4↔code check on every pre-existing import
edge. Either way adoption stalls. We need `nugit init` to make the *first* render
useful and non-hostile.

## Decision

1. **Reverse-engineer a first-pass model from the real Go import graph**, using
   the SAME analyzer the c4↔code check uses (`goimports.Analyze`). Because the
   generator and the checker share one analysis, the generated model matches the
   code *by construction* — the first render is green, not a wall of failures.
   One component per package directory; `paths "<dir>/**"`; one relationship per
   real cross-package import.
2. **Default `c4.mode` to `warn`** (warn-until-ratified). While a human reviews
   and trims the auto-generated model, undeclared edges warn rather than fail, so
   CI is never blocked during adoption. Flip to `enforce` once ratified.
3. **`config.yml` becomes load-bearing** — previously inert, it now drives the
   c4 mode and significance thresholds (`internal/config`).

## Consequences

- A brownfield repo is adoptable in one command and is useful immediately.
- The generated model and the check can never silently disagree, because neither
  hand-maintains a file→component mapping (ADR-0002) the other doesn't see.
- Go-only for now; cross-language bootstrap waits on the fitness-function epic.

## Rejected

- **Ship `init` as a bare scaffold with an empty/template model.** Rejected: the
  first `pr-render` then finds no architecture to check (zero value) or fails on
  every existing edge — the exact adoption gap this feature exists to close.
- **Generate the model from a separate, independently-maintained mapping.**
  Rejected: it would drift from the checker's own analysis, reintroducing the
  model-vs-code disagreement ADR-0002 was written to prevent.
- **Default to `enforce`.** Rejected: a freshly auto-generated model a human
  hasn't yet trimmed would hard-fail legitimate PRs on day one, training teams to
  bypass the check before they trust it.
