---
schema_version: 1
id: ADR-0062
type: decision
scope: claim
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:claim
  - constrains:cli
  - amends:ADR-0049
  - informs:ADR-0048
provenance:
  commit: seed
  citation: "Two merged PRs in a downstream repo, 2026-11-19 and 2026-11-20: on a branch named after its bead and never claimed, the first commit carried objective: and the next did not. In one PR the untrailered commit changed only the plan store's shard; in the other it was a one-line follow-up. Reproduced in internal/claim/stamp_test.go."
confidence: high
---

# ADR-0062 — A branch named after its bead is claimed by its name, on every commit

## Context

ADR-0049 resolves a branch's objective from its claim, and with no claim from
the first `objective:` on the branch's commits. A later change added a third
source: a bead id in the branch name (`feat/<bead-id>-…`), checked after the
commits. AGENTS.md and the skill say such a branch "is claimed by its name".
The commit-msg hook stamps from a claim or from the name, never from the
commits, because on a stacked branch the commits since the merge base include
the base branch's.

That order defeated itself. On a named, unclaimed branch the first commit was
stamped from the name. From then on the branch's commits named the objective,
so they answered first, and the hook does not stamp from commits. Every
commit after the first went up with no `objective:`, and the SessionStart
digest told the agent to claim an objective the name had already claimed.
It showed up on the commits nobody writes a trailer for by hand: one that
only touched the bead's line in the plan store, and the one that closed the
bead. Neither the shard-only diff nor the closed bead played any part.

A stacked branch named after its own bead had the same fault from its first
commit, because its base branch's commits already named another objective.

## Decision

**A branch's objective comes from its claim, then its name, then its
commits.** The name is this branch's own and the commits may not be, so the
name ranks with the claim. The hook stamps from a claim or a name, as before.
The commits still answer for a branch with neither, and still never decide a
stamp.

The stamping moves from `cmd/nugit` into `claim.StampFile`, so the hook's
whole path is tested against a real repo.

## Rejected

- **Stamp from the commits as well.** On a stacked branch it would stamp the
  base branch's objective on this branch's work.
- **Stamp from the commits when they agree with the name.** It fixes the
  first case and leaves a stacked, named branch unstamped.
- **Have the skill tell agents to claim anyway.** The name is documented as
  a claim. The resolver should agree with the documentation, and an agent
  that has to remember a command is the failure ADR-0049 removed.

## Consequences

- On a named branch whose commits name a different objective, the name wins,
  and the hook warns on each commit that names the other one. `nugit
  objective claim` settles it, because a claim outranks the name.
- `nugit objective` and the digest report `from branch` for a named branch
  for its whole life, not only until its first commit.
- Export scoping (n8o/nugit#100) reads the same resolver to pick the plan a
  `plan normalize -merge` export may change. It accepts every source, so it
  never missed an objective the way the hook did. But under the old order a
  stacked branch named after its own bead took the base branch's objective
  from the commits, and the export was scoped to the base's plan. This
  branch's own bead edits, such as the close, were then written as HEAD had
  them and only reported as outside the scope. With the name ahead of the
  commits, the export is scoped to the branch's own plan.
