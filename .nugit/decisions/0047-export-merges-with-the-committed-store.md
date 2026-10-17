---
schema_version: 1
id: ADR-0047
type: decision
scope: beads
status: proposed
created: 2026-10-17T00:00:00Z
relates_to:
  - constrains:beads
  - amends:ADR-0040
provenance:
  commit: seed
  citation: "feat/plan-normalize-merge; on a real pilot store, re-exporting one bead from a database behind main rewrote 35 plan files (27+/244-), reopening closes and deleting a 24-step plan"
confidence: medium
---

# ADR-0047 — An export merges with the committed store; a stale database cannot revert main

## Context

The committed `.beads/plans/*.jsonl` shards are the source of truth. The `bd`
database is a local cache, one per clone, shared by every worktree. Nothing
pulls other sessions' merged plan changes into that database, and `bd export`
serializes *every* plan from it. So a session that changes one bead and
re-exports from a database behind main writes main's state back as it was when
the database last imported: closes reopen, dependencies vanish, and whole plans
added since then are deleted.

On a real pilot store, fixing one bead's description re-exported 35 files
(27 insertions, 244 deletions). The export script's only defence is a printed
warning: "stage only your own plan". That is advice, and a session that follows
it still reverts the other beads in its own plan.

Timestamps alone cannot decide this. Measured with bd 1.2.2: `bd dep add` and
`bd label add` move no `updated_at`. A local label edit and a stale database
look the same to a clock.

## Decision

`nugit plan normalize -split -merge <ref>` folds the store committed at `<ref>`
into the fresh export before writing the shards.

- **With `-merge-base <file>`**, the previous raw export of the same database,
  this is an exact three-way merge per bead:
  - the database untouched since the base → main's line;
  - main untouched → the database's line;
  - both changed → merged field by field, with the database's value winning a
    true conflict (reported).
  - In the base but gone from the export means it was deleted here, so the
    deletion carries through. Gone from main and untouched here is deleted
    too.
- **Without a base** (first run):
  - only committed → kept (never delete what the database never saw);
  - else, when both carry `updated_at`, the later clock wins. The clock is the
    latest of `updated_at`, `closed_at` and the dependencies' `created_at`;
  - else a committed close is kept over an open export;
  - else the export wins.
- A bead stays in its committed shard unless it names a `plan`.
- `dependency_count`/`dependent_count` are derived: ignored when comparing and
  recomputed over the result. They matched the graph exactly on all 562 beads
  of the pilot store.

The base is the caller's to keep, not nugit's. It lives next to the database
(the git common dir), and the export script saves what it fed to normalize as
the next run's base. nugit stays a pure function of a ref, a working-tree
store and a file (ADR-0006).

## Rejected

- **Newest `updated_at` wins, with no base.** bd moves no clock for label or
  dependency edits, so this either reverts main's dependency changes or drops
  local labels, depending on how ties are broken. It is kept only as the
  first-run fallback.
- **Import main's shards into the database before every write.** `bd import`
  upserts, so it overwrites unexported local edits. In a store whose export
  strips run state (`stage:*` labels), it also wipes that state from the
  database.
- **Only write the shards of plans the session names.** That still reverts the
  other beads of the named plan, and it relies on every caller knowing which
  plans it touched.

## Consequences

- With a base, an export's diff is exactly this session's edits.
- A first run without a base can still carry a dependency removal or a derived
  count main does not have. The report lists every bead it took from main, so
  review the diff once.
- A true conflict (both sides changed the same field) keeps the database's
  value and is named in the output.
