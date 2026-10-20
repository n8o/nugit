---
schema_version: 1
id: ADR-0050
type: decision
scope: orphans
status: proposed
created: 2026-10-20T00:00:00Z
relates_to:
  - constrains:orphans
  - constrains:cli
  - constrains:gitutil
  - amends:ADR-0048
  - informs:ADR-0049
provenance:
  commit: seed
  citation: "first runs on three real repos, 2026-10: with a 1000-PR window a pilot repo reported 208 orphans, 194 of them branches whose PRs had merged; reading every PR left 45"
confidence: medium
---

# ADR-0050 — Orphans are found against objectives, and a PR's state decides merged

## Context

ADR-0048 promised a report of work that was started and left. The reviewer
does not run nugit, so the report must be something an agent can produce when
asked ("what's orphaned?") and present as decisions. Each finding needs a
choice between finishing and dropping, not just a list of facts.

On the first runs, git alone could not tell which branches were merged. A
squash merge never makes the branch's commits ancestors of the default branch.
Once the default branch moves on, neither a tree comparison nor patch ids
recognize the squash commit. On a pilot repo that mislabelled most old
branches as unmerged.

## Decision

**`nugit orphans` is a read-only report** in six sections, in the order they
need attention:

- **stalled objectives:** a bead that is in progress, claimed, or named by
  commits, still open, and quiet past the threshold (default 7 days);
- **stopped sessions:** a claim whose agent-deck session is stopped or gone
  while its bead is open;
- **closed with work left:** a closed bead whose branch still holds unmerged
  commits;
- **stray branches:** unmerged, quiet, and either with no PR, a closed PR, or a
  stale open PR;
- **stale notes:** `nugit remember` entries never recorded as knowledge;
- **merged leftovers:** branches whose work is already merged. This is cleanup,
  not a decision, so markdown collapses it into one line.

The plan store is read at origin's default branch, the state that counts.

**A PR's state is the authority on "merged".** When `gh` is available, one
`gh pr list --state all` call maps each branch to its newest PR. A MERGED PR
means merged, whatever git's history says. Without gh, a branch counts as
merged when it has no commits ahead or `git merge-tree` shows its content is
already on the default branch. The report then says GitHub was not consulted.

**agent-deck is consulted the same way.** `agent-deck list -json` gives
session state. Without it, the stopped-session section is skipped and the
report says so; claims are never guessed dead.

**The report never acts.** It names the decision. The reviewer chooses, and
the agent carries it out. The skill says not to delete a branch or close a
bead unasked.

## Rejected

- **Patch-id or tree matching alone for squash merges.** Measured on known
  merged branches, patch ids matched one in five. The tree check fails as soon
  as the default branch changes the same files.
- **A bounded PR window (1000).** On a repo with 2,700 PRs it hid every old
  PR. All of them take about 15 seconds to list, so the window is 10,000.
- **Remote branches.** Merged PR branches on the remote are usually deleted by
  GitHub, and the ones that are not would flood the report. Local branches and
  worktrees are what an agent leaves behind on a host.
- **A gate (exit non-zero).** Orphans inform a decision; they never block one.

## Consequences

- `internal/orphans`: Gather (git, store, claims, journal, gh, agent-deck),
  pure Compute, Markdown. `gitutil` gains Branches, Ahead, ContentMerged and
  LogTimed.
- Beads with no `updated_at` and no commits or claims cannot be aged, so a
  hand-written store's in-progress beads are reported only once worked.
