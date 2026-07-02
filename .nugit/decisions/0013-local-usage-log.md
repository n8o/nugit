---
schema_version: 1
id: ADR-0013
type: decision
scope: global
status: accepted
created: 2026-07-02T00:00:00Z
relates_to:
  - constrains:usage
  - constrains:retrieval
  - constrains:mcp
  - elaborates:ADR-0004
provenance:
  commit: seed
  citation: internal/usage/usage.go; JBS pilot review 2026-07-02
confidence: high
---

# ADR-0013 — Local, gitignored usage log for context(); `nugit stats`

## Context

The thesis' retrieval half claims `context()` helps agents, but nothing recorded
whether it was even *called*. The JBS pilot made the gap concrete: a well-fed
store (8 ADRs, 16 lessons) with no way to tell if any agent ever read from it.
Measurement has rungs — use, then usefulness — and rung one is observability of
calls. It must not compromise nugit's commitments: deterministic output, no
external datastore, canonical state as git text, no phone-home.

## Decision

1. Every served `context()` call (CLI and MCP) appends one JSON line to
   `.nugit/.cache/usage.jsonl` — already the designated gitignored, rebuildable
   local dir. Recorded: time, source, path, task, resolved component, bundle
   composition counts, token estimate/budget, truncation.
2. Logging is **best-effort at the edges** (`cli`, `mcp`), never inside
   `retrieval` — the library stays a pure projection, and a logging failure can
   never fail or alter a bundle.
3. `nugit stats` aggregates the log (calls by source, top components,
   truncation rate, **unresolved paths** = model coverage gaps, **empty
   bundles** = capture gaps), with `-since` and `-format json`.
4. Opt-out via `usage: {log: off}` in config.yml. Default on: the file is
   local-only, gitignored, and the entire point is measurement.

## Rejected

- **No logging (status quo)** — leaves "does this help agents?" permanently
  unanswerable; adoption decisions (e.g. JBS flipping enforce, tightening
  gates) stay vibes-based.
- **Remote/centralized telemetry** — violates the git-native, repo-local
  philosophy and adds a consent/privacy surface nugit doesn't need; the repo
  owner can aggregate their own JSONL however they like.
- **Logging inside the retrieval package** — makes the pure projection
  side-effectful and entangles every future caller (tests, engine reuse) with
  filesystem writes.
- **Committing usage data to the store** — usage is derived, per-machine,
  high-churn state; canonical `.nugit/` is durable knowledge only (ADR-0001).

## Consequences

- Adoption of the retrieval half is now observable per-repo; `nugit stats`
  gives the coverage/capture gap signals that direct where to grow the model
  and the store.
- Task text lands in the local log; it stays on the machine that issued it.
- Outcome-level measurement (does context() change agent behavior?) remains
  open — see the A/B eval harness (pr-render as deterministic judge).
