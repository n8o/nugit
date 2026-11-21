---
schema_version: 1
id: ADR-0064
type: decision
scope: beads
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:beads
  - constrains:cli
  - amends:ADR-0047
  - amends:ADR-0061
provenance:
  commit: seed
  citation: "a pilot repo, 2026-11-21: with ADR-0061 in place a scoped export changed only its own plan's file, out of 152. A bead that export listed as outside the scope was then dropped by a later export that named its plan, and a worktree's first export (no base) rewrote lines that differed only in how they spelled dependencies: 7 lines on one plan, 2 on another"
confidence: high
---

# ADR-0064 — nugit writes the merge base, and spelling is not an edit

## Context

ADR-0061 scoped an export to its objective's plans. On the pilot, that cut a
session's diff to its own plan's file. Two gaps remained, and the pilot
reproduced both.

**The base recorded edits the export had left out.** ADR-0047 has the caller
save the raw `bd export` as the next run's base. That was exact while every
export wrote every plan: once committed, HEAD held what the export said. A
scoped export writes nothing from the database outside its scope, but the raw
export still records those plans as the database has them. The base then says
HEAD and the database agree on lines HEAD never took. The next export that
names such a plan misreads all three kinds of edit:

- **A bead filed there** is in the base, not on HEAD, and untouched in the
  database. It reads as deleted on HEAD, so it is dropped.
- **An update there** is in the base and untouched since. It reads as a
  database behind HEAD, so the update is reverted to HEAD's line.
- **A `bd delete` there** is not in the base but is on HEAD. It reads as a
  bead the database never saw, so the bead is kept.

None of them is reported as lost, and bd still holds every edit. Keeping the
base per worktree does not help, because the session's own scoped export is
what wrote it.

**A worktree's first export rewrote lines no one changed.** Without a base,
the timestamp rules decide a bead that both sides carry differently. Two lines
that differ only in how they spell dependencies carry one clock, and a tie goes
to the export. The pilot's store has three spellings bd does not write:

- an empty `dependencies` list, where bd omits the key;
- a copy of an edge between two other beads, whose `issue_id` is another bead
  (bd counts an edge on its owner's line only, ADR-0061);
- edges in another order.

Replayed over every plan of the pilot store, a no-base export scoped to one
plan rewrote 10 lines across 6 plans. Six of those lines were these spellings.

## Decision

1. **nugit writes the next base.** With `-write`,
   `plan normalize -merge <ref> -merge-base <file>` replaces the file with the
   next base. It does so after writing the shards, through a rename, and
   creates the file when it is missing. The caller still chooses where the
   file lives, and no longer saves `bd export` over it. A dry run writes
   neither the shards nor the base.
2. **For each bead, the next base is the last line the database and the
   committed store share once the shards are committed.**
   - Inside the scope it is the export's line, as before (ADR-0047). HEAD takes
     whatever the export wrote, and where the merge kept HEAD's line the
     database has not moved from its own.
   - Outside the scope it is the previous base's line, or nothing, because
     the database's edits there are still pending. The exception is a bead
     that the database and HEAD agree on: their shared line becomes its base.
   - An unscoped export's next base is the raw export, line for line.
3. **Two lines are the same bead when they are equal apart from four things:**
   the derived counts, the order of the dependencies, an empty list (equal to
   no list), and edges another bead owns. Merge then keeps HEAD's bytes, with
   or without a base. The merge compares the `dependencies` field the same
   way field by field, so reordering it is no conflict.

## Rejected

- **The merged output as the next base** (HEAD's lines outside the scope,
  merged lines inside it). Wherever HEAD moved and the database did not, that
  base would hold HEAD's line. The next export would then read the stale
  database line as an edit and revert HEAD, which is the bug ADR-0047 fixed.
  Outside the scope it would happen to every bead the database is behind on,
  the first time an export names that bead's plan.
- **A sidecar file beside the raw export, holding the pending lines.** That
  means two writers of one base. Keeping the caller's raw export right would
  still be up to the caller, and getting it wrong would go unnoticed.
- **A separate `-next-base` output flag.** It is more explicit, but a caller
  that leaves it off keeps saving the raw export and keeps the bug, with no
  sign of it. Writing over the input the caller already names cannot be left
  off.
- **Rewriting the spellings into bd's form** (dropping the copies, sorting the
  edges). That would put those lines in one more diff, which is what this
  decision avoids. In a hand-merged store, a copy can also be the only record
  of an edge.

## Consequences

- An export script stops saving the raw export, because nugit's base replaces
  it. A script that keeps saving it gets ADR-0061's behaviour, gap included,
  and nothing worse.
- A bead filed, updated or deleted in another plan is carried by the first
  export that names its plan. Until then, each export lists it under "outside
  the scope".
- A bead outside the scope with no previous base line, as in a worktree's
  first export, gets one once the two sides agree or its plan is exported.
  Until then the timestamp rules decide it.
- Exporting twice before the first export's shards are committed still
  reverts the first export's edits (ADR-0061). The base still assumes HEAD
  takes each export.
- Without a base, a real difference under one clock still goes to the export:
  on the pilot, a description on one bead and `started_at` on two. A base
  settles them; timestamps cannot.
- On the pilot replay, no-base scoped exports dropped from 10 rewritten lines
  in 6 plans to 4 lines in 4 plans, each a real field difference. With a base
  it is still 0.
