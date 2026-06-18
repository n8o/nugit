---
schema_version: 1
id: ADR-0005
type: decision
scope: global
status: accepted
created: 2026-06-18T00:00:00Z
relates_to:
  - constrains:trailers
  - constrains:gitutil
provenance:
  commit: bootstrap
confidence: medium
---

# ADR-0005 — Capture survives squash-merge

## Context

GitHub's default merge strategy is squash-merge, which collapses per-commit
trailers into one combined message and orphans every `provenance.commit` SHA.
The review's completeness critic flagged this as a high-impact blind spot: the
most common real-world merge workflow silently breaks the capture-and-provenance
chain the entire system feeds on.

## Decision

1. **Render at PR time, before the squash** — `nugit pr-render` reads the PR's
   individual commits via `mergeBase(base,head)..head`, so trailers are captured
   while they still exist, and the knowledge delta is computed from that range.
2. **Promote durable knowledge into in-tree files within the PR**, not into commit
   messages. Files survive squash; commit messages do not. Trailers are a capture
   *signal*, never the durable store.
3. **`provenance.commit` accepts the PR's merge commit or PR number** as a stable
   anchor, since the original per-commit SHAs do not survive squash. Provenance is
   best-effort, not load-bearing for retrieval.

## Consequences

- Durability lives in files that survive any merge strategy.
- The capture step is timed before history is rewritten.
- Provenance degrades gracefully (PR anchor) instead of dangling on a dead SHA.

## Rejected

- **Store the why only in commit trailers.** Rejected: squash-merge destroys it;
  trailers must be a transient signal distilled into files within the PR.
- **Forbid squash-merge org-wide.** Rejected: nugit must not dictate a team's
  merge workflow; it adapts to the dominant one instead.
