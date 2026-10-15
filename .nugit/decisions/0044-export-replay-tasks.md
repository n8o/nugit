---
schema_version: 1
id: ADR-0044
type: decision
scope: global
status: proposed
created: 2026-10-15T00:00:00Z
relates_to:
  - constrains:replay
  - constrains:retrieval
  - constrains:knowledge
  - informs:ADR-0027
  - informs:ADR-0040
  - informs:ADR-0006
provenance:
  commit: seed
  citation: "feat/export-replay-tasks"
confidence: medium
---

# ADR-0044 — Export a repo's own history as replay benchmark tasks

## Context

The agent A/B harness under `evals/` needs tasks to run an agent against with
and without nugit's context, and the only honest source of tasks for a repo is
that repo's own past work: a real request, the real starting tree, the real
change that satisfied it. SWE-bench established the shape — an issue, a base
commit, the solution diff, and the tests the solution added, withheld from the
agent and applied afterwards to grade it.

A repo driven through a Beads plan store already holds the issue half in a form
better than an issue tracker's: a bead's `description` is written **before** the
work by whoever planned it (it often ends in a "Done when …" sentence), and
`close_reason` / `closed_at` record when it finished. The commits that did the
work name the bead (`closes gr-12-1`, `Bead gr-12-1`, the id in the subject).
Joining the two is a pure function of git — exactly the derived-artifact shape
[[0027-export-skillopt]] took for lessons.

The trap is the same one ADR-0027 named: **a task is worthless if its inputs
reveal its answer**, and a replay task has a sharper version of it. The things a
harness scores nugit on — which decisions an agent should have consulted, which
alternatives it should not have re-proposed — are read from the knowledge store,
and the solution under test routinely *edits that store*: it adds the ADR for
the change it makes. Reading the store at the tip, or from the working tree,
hands the agent's grader the decision the agent was supposed to arrive at.

## Decision

1. **`nugit export -format tasks`**, a second format on the ADR-0027 verb,
   backed by a new `internal/replay` package. Output is one pretty JSON array
   (stdout or `-o`); the counts-by-reason summary goes to stderr and `-report`
   writes it as JSON.
2. **A task is a closed bead joined to the first-parent commits on the target
   branch that mention its id as a whole token** — `gr-12` never matches inside
   `gr-12-1`, so an epic does not claim its steps' commits. A side-branch commit
   counts for the merge that introduced it, so the merge layout links as well as
   the squash layout. The target branch is `-branch`, else the first of
   `origin/HEAD`, `origin/main`, `origin/master`, `main`, `master` that resolves.
3. **The solution is one unit of work.** A commit that touches only `.beads/` or
   `.nugit/` (the bead-closing commit) is recorded but never decides the range.
   One implementing commit → `squash` (parent is base) or `merge` (first parent
   is base, the merge is the solution). Several → a `range` from the earliest's
   parent to the latest **only when nothing unlinked landed between them**;
   otherwise the single commit that most plainly is the work wins (id in the
   subject, then a closing verb before the id, then the latest). A bead is
   commonly named again by a follow-up weeks after it closed, and spanning to
   the follow-up hands the agent every unrelated commit in between — measured on
   a real pilot store this was the difference between a 53-file and a 2,025-file
   "solution".
4. **The leakage rule.** `prompt`, `oracle_keys`, `rejected` and
   `decision_density` are computed from the repo **as of `base`** — the store via
   `knowledge.LoadAtRef`, the model and config via `ShowFile` at base, and the
   prompt from the bead's record at base when it already existed there
   (`source.prompt_ref: base`), so a description edited after the work never
   becomes the prompt. An oracle key must exist in the store at base; a decision
   the solution itself added is **never** an oracle (it did not exist when the
   work started), though what it `relates_to` / `supersedes` may be. Keys cited
   in the solution commits' messages and trailers, and the ids of decision files
   the solution modified or deleted, complete the set.
5. **`decision_density` uses retrieval's own scoping**, exported as
   `retrieval.ScopedDecisions`, over the model and store read at base — so the
   number means "what context(path) would have admitted then" and cannot drift
   from what context() serves today. Because the no-task scope admits every
   global decision, `decision_density_scoped` reports the component-scoped and
   path-bound part separately; it is the part that varies by path.
6. **Classification is a table, not judgment.** Test files by convention
   (`_test.go`, `*.test|spec.{ts,tsx,js,…}`, `test_*.py`, `*_test.py`,
   `*Test*.cpp|cc`, `*_test.cpp|cc`, `test/`, `tests/`, `spec/`, `__tests__/`)
   become `hidden_tests` (added or modified, never deleted). `.beads/` and
   `.nugit/` are never solution files. Skips are reported by name:
   `no-linked-commit`, `docs-only`, `tests-only`, `no-base`.
7. **`verify` is always empty.** The harness owns how a task is run and checked;
   the exporter only says what changed and what was withheld.
8. **Reading a store at many historical refs batches through one
   `git cat-file --batch`** (`gitutil.ShowFiles`). `LoadAtRef` and `beads.Read`
   use it too; their output is unchanged.

## Rejected

- **Mine GitHub PR and issue text instead of beads.** It needs the network,
  which the keystone does not take (ADR-0006), and the PR body is written
  *after* the work by the person who did it — it describes the solution, so as a
  prompt it leaks the answer by construction. A bead's description is written
  before the work, and it is in git.
- **Have an LLM write the prompt from the diff.** Nondeterministic (the same
  history yields a different benchmark each run, so a score change cannot be
  attributed), costly per export, and it reads the solution to describe the
  problem — the leak this ADR exists to prevent, laundered through a model.
- **Read oracle keys from the tip or the working tree.** Simpler, and wrong in
  the one way that matters: the solution's own new ADR becomes the oracle for
  the work that wrote it, and every with-nugit score is inflated.
- **Span from the first to the last commit that ever mentions the bead.** The
  brief's first cut. It turns one late follow-up into a solution containing
  weeks of unrelated work (Decision 3).
- **Match bead ids as substrings.** `gr-12` would link every `gr-12-*` step's
  commit to the epic, and an epic would become a task whose solution is the
  whole plan.

## Consequences

- Yield is a legible measure of commit hygiene, the way ADR-0027's yield is of
  capture hygiene. On a real pilot store: 406 closed beads → 208 tasks (161 with
  hidden tests); 174 closed beads are never named by any commit and are skipped
  by name rather than guessed at.
- A bead created in the same PR that implemented it has no record at base, so
  its prompt comes from the tip (`prompt_ref: tip`) — still the planner's text,
  but no longer provably pre-implementation. Consumers that need the strict
  guarantee filter on `prompt_ref`.
- `hidden_tests` includes fixtures under test directories, not only test
  sources. That is what a harness must withhold anyway.
- The output is deterministic (newest solution first, id breaking ties) and the
  schema is a contract with the harness: fields may be added, never renamed.
