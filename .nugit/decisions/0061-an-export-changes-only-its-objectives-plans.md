---
schema_version: 1
id: ADR-0061
type: decision
scope: beads
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:beads
  - constrains:cli
  - amends:ADR-0047
  - informs:ADR-0048
  - informs:ADR-0049
provenance:
  commit: seed
  citation: "a pilot repo, 2026-11-21: its bd database was 224 beads behind HEAD, and every export (with the merge base in place) rewrote five plan shards (~21 lines) besides the session's own. Merging the previous export, unchanged, against HEAD reproduced the same five shards; at 1,031 beads, six derived counts disagreed with a full recount and two committed lines were in another writer's spelling"
confidence: high
---

# ADR-0061 — An export changes only its objective's plans

## Context

ADR-0047 made `plan normalize -split -merge <ref>` a three-way merge against
the previous export of the database, so that "the diff is exactly this
session's edits". On a pilot store that did not hold. With the base in place,
every export rewrote five plan shards no session had touched, and the session
reverted them by hand each time.

There are two separate causes.

**The merge rewrote lines it took from HEAD unchanged.** Merging the previous
export *as is* (a database untouched since its base) against HEAD should
reproduce HEAD. On the pilot it rewrote the same five shards:

- **Re-encoding.** Every line went back through nugit's encoder, so a
  committed line in another writer's spelling changed: keys out of sorted
  order, or a raw `>` that Go writes as `>`.
- **Recounting.** `dependency_count` and `dependent_count` were recomputed
  over the whole merged graph. Three lines carry a copy of another bead's edge
  (its `issue_id` is the other bead), which bd counts once, for its owner. The
  recount counted it once per line it appears on. Three more counts on HEAD
  were already stale against HEAD's own graph: an edge was added by a PR that
  did not rewrite the other bead's line. A full recount "fixed" all six, in
  plans nobody was working on. ADR-0047 measured the counts matching the graph
  on 562 beads; at 1,031 they no longer did.

**The base cannot tell sessions apart.** The database is one per clone and
shared by every session. The base is saved there too, so it records what the
*database* looked like at the last export by *anyone*. Everything any session
changed since then looks like this session's edit. Another session's `bd
create` or `bd update`, not yet exported, is carried into this session's diff.

ADR-0047 rejected scoping the export to named plans for two reasons: it still
reverts the other beads of a named plan, and it relies on every caller knowing
which plans it touched. Both are answered now. Inside the scope the
three-way merge still runs, so nothing in it is reverted. And nugit already
knows the session's plan: a branch's objective (ADR-0048) comes from its
claim, its commits or its name (ADR-0049).

## Decision

1. **A line the merge takes from the committed store unchanged keeps its
   bytes.** That covers a bead the database agrees with, one it is behind on,
   and one it never saw. `Normalize` writes such a line back as read, in the
   shard it was read from. A shard holding only such lines also keeps its line
   order.
2. **A derived count moves only by the edges this merge moved.** The change is
   measured against the side the line came from: HEAD's graph for a committed
   line, the export's graph for the export's line. An edge is identified by its
   owner (`issue_id`), the way bd counts it. Only a line merged field by field,
   which is in the diff anyway, is recounted outright.
3. **The export is scoped to the plan of the branch's objective.** That plan
   is where HEAD's store puts the objective bead, else where the export puts
   it: its `plan` field, its shard, or its id family (ADR-0040). A bead in any
   other plan is written exactly as HEAD has it, counts and all. A bead HEAD
   does not have is not written. Nothing from the database reaches those
   plans. The report names every bead there that an unscoped merge would have
   changed, so a session's edit outside the scope is never dropped silently.
4. **`-plans p,…` widens the scope** for a PR that spans plans. It adds to
   the objective's plan, and it is comma-separated and repeatable.
   `-plans all` exports every plan.
5. **With no known objective, the export is not scoped.** That covers no
   claim, no `objective:` commit and no bead in the branch name, and also an
   objective that is in neither store. The export does what ADR-0047 does,
   with decisions 1 and 2, and prints one line saying it was not scoped.

## Rejected

- **Recounting every derived count (ADR-0047's rule).** It corrects drift
  this session did not cause, so the correction lands in a diff that should
  contain only this session's edits.
- **Choosing a canonical spelling that matches the other writers** (for
  example, turning off HTML escaping). Whatever spelling is chosen, some writer
  disagrees with it: bd, a hand merge, or an older nugit. The fix that always
  works is not to re-encode a line nobody changed.
- **Scoping to the objective bead alone.** A step's work also touches its
  plan's other beads: it closes one, files the next, adds a dependency. The
  plan is also the unit the PR view renders (ADR-0040).
- **Adjusting another plan's derived count for an edge into it.** That puts
  a file outside the scope into the diff to keep a derived field exact. The
  scope is a promise about which files change.
- **Letting the newer clock win when HEAD differs from the base but the
  database does not.** That case is ambiguous. HEAD may have moved, or the
  base may hold an edit HEAD never got. A clock rule would undo a deliberate
  revert on main. It would also miss label edits, because bd moves no clock
  for those (ADR-0047).
- **nugit choosing and keeping the base itself.** The base stays the
  caller's file, and nugit stays a pure function of a ref, a working-tree store
  and a file (ADR-0006, ADR-0047). Where the caller keeps it is the caller's
  call; see the consequences.

## Consequences

- An export from a database untouched since its base reproduces HEAD byte for
  byte. Replayed on the pilot, 0 of 147 shards changed, down from 5.
- A scoped export's diff touches only the objective's plans. Another session's
  unexported edits *in the same plan* still ride along. Within one plan, the
  base still cannot tell two sessions apart.
- An edge from the objective's plan into another plan leaves that bead's
  derived count one behind. Merge ignores derived counts when it compares
  lines.
- A bead the session filed in another plan, such as a follow-up, is not
  exported until that plan is named with `-plans`. It is listed under
  "outside the scope" on every export until then.
- The base must be an export that HEAD has taken. Suppose another session
  exports between this session's `bd` edit and its own export, with the base
  kept once per clone. The edit is then already in the base, so it reads as
  "untouched here", HEAD's line wins, and it is reported as "database behind
  HEAD". Exporting a second time before committing the first export's shards
  does the same. A caller that keeps the base per worktree (under `git
  rev-parse --git-dir`, not `--git-common-dir`) closes the first case. The
  cost is that a worktree's first export has no base, and so falls back to the
  timestamp rules, inside the scope only.
