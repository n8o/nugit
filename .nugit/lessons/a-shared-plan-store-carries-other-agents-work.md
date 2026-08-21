---
schema_version: 1
id: LESSON-a-shared-plan-store-carries-other-agents-work
type: lesson
scope: beads
status: active
created: 2026-08-21T00:00:00Z
relates_to:
  - constrains:beads
  - constrains:delta
  - informs:ADR-0040
provenance:
  commit: seed
  citation: "pilot repo: .beads/issues.jsonl, 67 epics / 17 plans / 20 worktrees; a bead-only PR closed unmerged after another session's export carried all four of its closes to master"
confidence: high
---

# Lesson — a shared plan store means a PR's diff carries work its author never did

**Trigger:** reading, diffing, or attributing a `.beads/` plan store on a repo
where more than one agent session is active.

**Insight:** `bd` keeps **one database per repository**, resolved from the main
checkout even when run from a linked worktree — so under one-worktree-per-task
every concurrent session writes the same store, and `bd export` serializes the
*whole* database, not the rows that session touched. Two consequences follow,
and both read as mysteries until you know this:

- **A bead-only PR can be overtaken and become a no-op.** On the pilot, a PR
  closing four beads surfaced as a conflict on `issues.jsonl` because another
  session's export had already carried all four to `master`. The correct
  resolution was to close it unmerged; resolving the conflict would have landed
  an empty change. Diff by *id and status* before resolving — if no bead exists
  only on your branch and none differs in status, the PR is redundant.
- **Lines in a store diff do not belong to the PR that carries them.** Never
  attribute plan movement from the raw diff; compare the parsed store at base
  against the parsed store at head, per bead, which is what `delta.DiffPlan`
  does.

**Rejected:** isolating the store per worktree. It trades this confusion for
divergent plans that reconcile only through git conflicts, and destroys the one
thing a shared store buys — one session seeing that another already took a step
or is blocked on it. The fix belongs in how the store is *serialized* (one
stable line per bead, one file per plan — `nugit plan normalize`) and in how it
is *rendered* (scope to the plans this PR moved — ADR-0040), not in splitting
the database.

**Keywords:** beads, bd, plan store, worktree, export, merge conflict, agent
fan-out, attribution, issues.jsonl
