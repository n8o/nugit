---
schema_version: 1
id: LESSON-a-pure-function-of-base-and-head-inherits-head-s-staleness
type: lesson
scope: consistency
status: active
created: 2026-10-08T00:00:00Z
relates_to:
  - constrains:consistency
  - informs:ADR-0041
  - amends:LESSON-read-from-reviewed-ref
provenance:
  commit: seed
  citation: "pilot repo: ADR-JBS-0051 carried by 3 files on master, merged 11:02/12:19/14:00 on 2026-08-27 from three bases, every PR green"
confidence: high
---

# Lesson — a check that is a pure function of (base, head) inherits head's staleness

**Trigger:** a PR-time check that must answer a question about the branch being
merged INTO — is this id free, is this name taken, does this registration
already exist — rather than about what the PR changed.

**Insight:** reading everything from the reviewed ref is the right default
(LESSON-read-from-reviewed-ref) and it is not sufficient. A head that has not
merged the target cannot contain anything a sibling PR landed after the branch
was cut, so a uniqueness check evaluated there has nothing to collide with and
passes honestly. Re-running CI does not close the window: a `pull_request`
workflow fires on a push to the *head* branch, never on the base branch
advancing, so "green" has always meant *green against the base as of the last
push to this branch*.

Anything that behaves like a shared mutable counter — a hand-assigned
sequential id, a port number, a migration index — turns that window into a
collision generator. On the pilot three branches cut before `ADR-JBS-0051`
existed all saw 0050 as the highest, all minted 0051, and all three landed
within three hours with every check green.

Distinguish the two kinds of question:

- **"What changed?"** — measured against `mergeBase(base, head)`. Any other
  reference point reports a sibling's work as this PR's.
- **"Will this fit?"** — measured against the target branch's **tip at render
  time**. The merge base cannot answer it, because everything that landed after
  the divergence is invisible there.

**Rejected:** requiring branches to be up to date before merge. It works, and
it is a policy the tool cannot set for its adopters — and it costs a rebase per
sibling merge on a repo landing ten PRs a day. Reading the target tip is one
git read and is correct however stale the branch is.

**Keywords:** pr-render, merge base, target tip, staleness, uniqueness, id
collision, concurrent pull requests, shared counter, determinism, github
pull_request event
