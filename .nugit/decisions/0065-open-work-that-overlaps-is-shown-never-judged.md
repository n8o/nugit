---
schema_version: 1
id: ADR-0065
type: decision
scope: global
status: proposed
created: 2026-11-22T00:00:00Z
relates_to:
  - constrains:overlap
  - informs:ADR-0041
  - informs:ADR-0006
  - informs:ADR-0050
provenance:
  commit: seed
  citation: "nugit's own #93 and #94, 2026-11-21: both added ADR-0055, both green, merged 58 s apart; replayed at main before either merged, `nugit overlap` on #93's head names the collision with #94's branch. A large pilot repo, 2026-11-22: 12 open PRs and 106 remote branches; `nugit overlap -pairs` read 18 pieces of open work and found 26 overlapping pairs in 10.7 s, and the PR view section added 15 comparisons to one PR's render. Before finished PRs' branches were excluded, a branch left at a closed PR's head conflicted with five sibling PRs, and on nugit a branch rewritten seconds before its PR merged (#95) read as open work conflicting in four files. Prior art: Graphify `prs --conflicts` (communities, not a declared model)"
confidence: high
---

# ADR-0065 — Open work that overlaps is shown, never judged

## Context

Every check nugit runs is about one change. ADR-0041 widened the reference
point to the target branch's tip, so a PR that takes an id main already
holds fails. It cannot see what is still open. #93 and #94 both added
ADR-0055; both were green, they merged 58 seconds apart, and main carried
two records under one id until #104 renumbered one. While both were open,
nothing in either PR's view could show the other.

The same blindness covers ordinary work. Two PRs that rewrite one function
conflict only for whoever merges second. Two that change one component
without sharing a file are each reviewed as if they were the only change
to it. A reviewer or an agent learns about the other only by scanning the
open PR list by hand, which the pilot did on the 2026-11-12 collision by
listing every remote branch's `.nugit/decisions/`.

Graphify's `prs --conflicts` answers the question with statistical
communities of a code graph. nugit already has a better vocabulary: the
declared C4 model, whose path globs map every file to a component.

## Decision

1. **`nugit overlap` lists the open work that touches what a branch
   touches.** For each piece of open work it reports what the two changes
   share:
   - **store keys** both add: one record id, or one decision number in one
     directory and id namespace, the key `duplicate-decision-number`
     enforces once both have merged;
   - **files** both change, and which of them a merge of the two would
     conflict in (`git merge-tree --write-tree`, nothing written);
   - **components** both change, by name.

   Items are ordered by what they break: a key collision, then a conflict,
   then shared files, then shared components. `-pairs` compares every two
   pieces of open work with each other instead of with one branch.
   `-format json` carries the same view.

2. **A change is what lies past its merge base**, for the branch and for
   every piece of open work, each against its own target (a stacked PR is
   measured against the branch it targets). What the target gained after a
   branch was cut is not that branch's change.

3. **One model maps every change:** the reviewed head's. "The same
   component" then means one thing on both sides, including a component
   the branch itself declares.

4. **Open work is read at the CLI edge, never in the engine.** The open
   pull requests come from `gh`, as `nugit orphans` reads them (ADR-0050).
   Branches with no open PR come from the remote refs this checkout has
   already fetched, when pushed within `-recent` days (14). Nothing is
   fetched. Left out:
   - work on the branch's own line: an ancestor of it or a branch built on
     it, as a stack is;
   - a branch a PR targets, and the remote's default branch;
   - a branch already in the target (`Ahead` 0, or `ContentMerged`);
   - a branch a closed or merged PR left behind: still at the commit the
     PR ended at, or with no commit newer than the PR's close. A branch
     committed to after its PR ended stays.

   A fork's PR whose head is not in the checkout is named as unread. With
   no `gh`, the view says it read branches only.

5. **`pr-render -overlap` adds the view to the PR view, as a section only.**
   It is not a finding, it does not move the significance class, and it
   never changes the exit code. Without the flag the output is
   byte-identical. In a pull-request workflow the target is
   `GITHUB_BASE_REF` and the PR's own branch `GITHUB_HEAD_REF`, so a
   caller that passes the merge base as `-base` still gets the right
   target. The section is open when anything collides, conflicts or shares
   a file.

## Rejected

- **A finding (warn or fail) for a key collision with an open PR.** The
  verdict would depend on what other people have open: the same base and
  head would pass on Monday and fail on Tuesday, and re-running a check
  could not reproduce it. The PR that has to act is the one that merges
  second. Once the other lands, ADR-0041's target-tip check fails it with
  the rename to make. Until then the collision leads the section.
- **Graphify's communities as the unit of "the same area".** A statistical
  clustering is not a component model (ADR-0012), and on the pilot it
  produced 3,593 of them. The declared model's globs are explainable and
  already gate the PR.
- **`gh` or the GitHub API inside the engine.** The deltas, significance
  and checks stay pure functions of committed text (ADR-0006, AGENTS.md).
  The overlap view is read by the CLI and handed to the renderer.
- **Overlap in the `context()` bundle.** context() runs before every edit
  and must stay offline and fast; a `gh` round trip per call is the wrong
  cost, and the bundle is about knowledge, not other people's work.
- **Fetching the open PRs' heads** (`refs/pull/*/head`). It fetches every
  PR ever opened. A checkout that wants a fork's PR fetches it itself; the
  view names what it could not read.
- **All remote branches, however old.** On the pilot most remote branches
  are long finished. The 14-day window, plus the finished-head and
  merged-content tests, keeps abandoned work out. `-recent` widens it.

## Consequences

- An agent can run `nugit overlap` before it takes a decision number or
  opens a PR, and see another branch's claim on the same number while
  both are open. The skill says so.
- Two PRs that each add a step to one plan shard insert their lines at the
  same place, and the merge simulation reports the shard as a conflict.
  That is what GitHub reports once the first merges; per-plan shards keep
  different plans apart, not two new steps in one plan.
- A branch pushed after its PR merged (commits the merge did not take) is
  listed as a branch with no open PR. git alone cannot tell it from new
  work; the label says what it is.
- A broad component (a whole frontend) makes many PRs "share" it. Those
  items sort last and carry no icon.
