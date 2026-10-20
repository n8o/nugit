---
schema_version: 1
id: ADR-0048
type: decision
scope: global
status: proposed
created: 2026-10-19T00:00:00Z
relates_to:
  - constrains:objective
  - constrains:trailers
  - constrains:render
  - constrains:engine
  - amends:ADR-0040
  - informs:ADR-0045
  - informs:ADR-0042
provenance:
  commit: seed
  citation: "operator review, 2026-10: one interactive agent session ran six unrelated objectives across three repos; its PRs had no plan movement, so the plan section scoped by ADR-0040 had nothing to say about what each was for"
confidence: medium
---

# ADR-0048 — One session, one objective: a PR names what it is for

## Context

The reviewer of a nugit repo does not run nugit. An agent writes the code and
operates the tools; the reviewer reads the PR view and nothing else. The PR
view is therefore the reviewer's whole picture of the change. It should say what
the change is FOR as well as what it touched.

ADR-0040 scopes the plan section to the plans a PR moved. That works when the
change opens or closes a bead. Most interactive agent work does neither: the
session picks up a task, edits code, and opens a PR that moves nothing in the
store. That PR's view has no objective and no frame for its findings. A PR that
mixes two tasks looks the same as a focused one. One real session ran six
unrelated objectives under one title, and nothing in any of its PRs showed it.

Work also gets abandoned. A bead stays in progress after its session stops, a
branch keeps unmerged commits with no PR, a finding sits in a local journal.
Nothing reports these. Without a declared objective there is nothing to report
them against.

## Decision

**An objective is a bead, and a commit names it with an `objective:` trailer.**
`objective: <bead-id>` goes on every commit an agent makes for that objective.
It is a typed trailer field like `spec:`. Unlike the capture fields, it does
not make a commit a capture block: a commit that only names its objective
needs no `learned:` or `keywords:`.

**One session works one objective.** An agent-deck session claims one bead when
it starts and names it on its commits. Unrelated work goes to a new session and
a new branch, not into the current PR. Later PRs teach the agent this (a
SessionStart hook and the nugit skill). This PR adds what they need.

**The PR view leads with the objective** when the range names one:

- the bead: id, title, status at base and at head, acceptance criteria;
- its plan's progress, so the PR shows where it sits in the arc;
- the components its commits touched (the objective's scope in this PR);
- the live decisions and lessons that govern that scope, with global ones left
  out because they apply to everything and tell the reviewer nothing about this
  objective.

A range that names no objective renders exactly as before. The section is
absent, and so is every finding below, so an existing repo's output does not
change until its agents start naming objectives.

**`objective-focus` keeps PRs on one objective** (warn):

- a range that names more than one objective;
- files changed only by commits that name no objective, in a range where other
  commits do. A file that an objective commit also touched is in scope, so a
  follow-up fixup commit without the trailer does not trip it;
- an objective id that the plan store at head does not contain, when the repo
  has a store.

Scope is derived from the commits, not declared in advance. The agent cannot
know before the work which components it will touch, and a declared scope is
one more thing to drift.

**Orphans are found against objectives** (a later PR, `nugit orphans`): beads
in progress with no recent commits or open PR, stopped sessions whose objective
is open, unmerged branches with no PR, merged work whose bead stayed open, and
unpromoted findings. The reviewer decides to finish or drop each one; the
agent does the work.

## Rejected

- **Infer the objective from the branch name.** Branch names are free text, and
  greenhouse and agent-deck name branches differently. The trailer survives a
  rebase and is checked in the same place as every other capture field.
- **Declare the objective's components up front.** It is a guess before the
  work starts, and a wrong guess turns every real change into a warning.
- **Fail on an unfocused PR.** Start at warn, as every new consistency check
  does (the convention in AGENTS.md). Promote once agents name objectives
  reliably.
- **Make the objective a new knowledge type.** The plan store already holds the
  task, its status and its plan. A second record of the same task would drift.
- **Ship the session plugin first.** The PR view is the only thing the reviewer
  reads. Until it can show an objective, teaching agents to declare one has no
  visible effect.

## Consequences

- Phase 1 (this decision's first PR): the `objective:` trailer, the
  `internal/objective` package, the PR view's Objective section and the
  `objective-focus` check.
- Phase 2: a SessionStart hook and a skill rule so agents claim an objective
  and keep one per session.
- Phase 3: `nugit orphans`.
- greenhouse already runs one bead per session. Its stage sessions name the bead
  on their commits, so its PRs get the section once the prompts add the
  trailer.
