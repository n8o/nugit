---
schema_version: 1
id: ADR-0059
type: decision
scope: global
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:reverts
  - constrains:cli
  - informs:ADR-0044
  - informs:ADR-0057
  - informs:ADR-0058
  - informs:ADR-0056
  - informs:ADR-0006
provenance:
  commit: seed
  citation: "Claude Code team, 'How the Claude Code team thinks about verification' (2026-11): track reverted merges, maintainer-rejected changes and a size-matched clean subset; evaluate on past incidents and reverts with a one-sentence answer key, merged-and-stable changes as a control, a tuning set split from a fixed test set, and recall measured separately for raw findings, post-verification and final review. Owner's rulings on this record, 2026-11-21: build the revert-rate report now; per-stage recall waits for it; GitHub-side signals stay out of nugit's core."
confidence: medium
---

# ADR-0059 — Review the reviewers: count reverts per month first, measure recall per stage only once they show something

## Context

nugit now has several reviewing stages. context() serves the decisions and
lessons in scope for a file. pr-render's consistency checks flag a range.
Checklist items ask diff-triggered questions (ADR-0057). The verify gate
stamps commits with how they were checked (ADR-0056). Each stage was built
with tests that prove it runs. None of them is measured on whether it
catches what mattered.

The Claude Code team grades its reviewers three ways:

- **Outcomes over time:** how many merged changes were reverted, how many a
  maintainer rejected before merging, and a size-matched subset of changes
  that stayed clean.
- **A formal evaluation on past incidents and reverts.** The repo is
  recreated as it was at review time, with a one-sentence answer key per
  example. Merged, stable changes are the control against over-flagging, and
  a tuning set is kept apart from a fixed test set.
- **Recall per stage** (raw findings, post-verification, final review), so a
  system that finds a bug and then loses it is visible. One end-to-end score
  hides that.

nugit already holds most of the inputs. Git history has the reverts.
ADR-0044's replay tasks are past work, reconstructed at its base with oracle
keys: the decisions the work should have consulted, read from the store as of
base. `internal/eval` already scores pr-render's checks for precision and
recall over a hand-labelled corpus. What is missing is a measure tied to this
repo's own outcomes.

This record was first written as a proposal with three options and three
questions. The owner ruled on 2026-11-21, and the decision below is that
ruling.

## Decision

### 1. Outcomes per month (`nugit stats -reviewers`): built now

A new `internal/reverts` component reads the default branch's first-parent
history and reports, per calendar month:

- **merged changes:** first-parent commits, a merge commit sized by its diff
  against its first parent.
- **reverts:** commits whose body says `This reverts commit <sha>`. Each is
  joined to the merged change that brought the reverted commit in (the
  first first-parent commit on the ancestry path from it), with that change's
  age when it was reverted. A revert of a revert is a re-land, not a new
  revert, and is not counted.
- **revert rate:** reverted changes over merged changes, by the month the
  change merged, so a month's rate can still rise as late reverts land.
  The reverted changes' size (files, churn) is shown next to the month's
  median.
- **a size-matched clean set:** for each reverted change, the merged change
  from the same month nearest to it in churn that stayed clean. Clean means no
  revert, and no `fix` commit (`fix:` or `fix(scope):`) touching one of its
  files, within 30 days of merging. A change younger than 30 days at the tip
  is not yet clean. Ties go to the nearer file count, then the SHA. This set
  is the control that per-stage recall would need.

"Now" is the tip commit's date, not the clock, so the same ref gives the
same report. The scan is bounded the way every history scan is: a window
(`-months`, default 6) and a commit cap. `-json` gives the same report as
data. It is git-only and deterministic, so it sits beside `hygiene` and off
the delta path.

### 2. Recall per stage waits for the revert-rate report

Per-stage recall over replay tasks (option B of the proposal: in scope,
served, asked and flagged, over reverts and controls, with a stable tune/test
split) is **not built yet**. It needs a context() that reads at a ref, about
500 lines, and it is only worth that if the stages are missing things that
matter. The revert-rate report is what says whether they are. B is reopened
by a new decision that cites the report: reverts frequent enough, or of
changes the stages had in scope, that knowing which stage lost them would
change what gets tuned. The design is kept below so that decision can start
from it.

### 3. GitHub-side signals stay out of nugit's core

Rejected PRs and review comments are GitHub data, and reading them needs the
network. nugit's core does not read them, and no `gh` call is added to it
(ADR-0006). Each repo's CI feeds them in. `nugit stats -reviewers -json` keys
every month by `YYYY-MM` and every change by SHA, so a CI step can count
rejected PRs per month with `gh` and set them beside the merged and reverted
counts, or join review comments to the changes they were on, without nugit
knowing GitHub exists.

## The deferred design (B), for the decision that reopens it

Two labelled sets, both built from history:

- **incidents:** the changes the report found reverted. The answer key is
  the revert's own message, so a person writes it once and nugit never
  generates it.
- **controls:** the report's size-matched clean changes.

For each example, rebuild the state at its base (ADR-0044's leakage rule:
store, model and config read at base) and record what each stage did:

| stage | what it did on the example |
|---|---|
| in scope | records `retrieval.ScopedDecisions` admits for the changed files at base |
| served | of those, what context() would have served after ADR-0043's relevance cap |
| asked | checklist items the change's diff triggers (ADR-0057) |
| flagged | pr-render findings on the range, by check and severity |

Per stage, report the **flag rate on incidents against controls**. For replay
tasks with oracle keys, report the **oracle-key recall at "in scope" and at
"served"**. The gap between those two is "found it, then lost it": the
relevance cap dropping a decision the work needed. Examples split into
**tune / test** by a hash of their id, so the split is stable and nobody
picks it. Whether a finding correctly describes the bug in an answer key
needs judgement. A repo that wants that runs a model over B's JSON in its own
harness (`evals/agent-ab` is the precedent).

### Severity of what is caught: no change

The article separates must-fix, should-fix and nit, and code review tags
pre-existing problems. nugit's findings already carry fail and warn, and
ADR-0041 and the SIGNAL-1 work made PR-view findings base-relative, which is
the pre-existing rule. Nothing to add.

## Rejected

- **Building B now.** Its cost is a ref-reading context(), and nothing yet
  says the stages are losing what matters. Counting reverts first is cheap
  and tells us whether to pay it.
- **An opt-in `gh` read in nugit, like `narrative`.** It would put a network
  path and GitHub's API shape into the keystone for a number CI can already
  compute next to the report.
- **One end-to-end score.** It cannot tell a stage that never finds a
  problem from one that finds it and then drops it.
- **Generating answer keys with a model.** The key is what grades the
  stages, and a model-written key grades the model. The revert message is
  already written by the person who reverted.
- **Grading on the hand-built `internal/eval` corpus alone.** It tests that
  the checks do what their authors meant. Only the repo's own reverts test
  whether that was what mattered.

## Consequences

- New `internal/reverts` component, `cli -> reverts` and
  `reverts -> gitutil` in `workspace.dsl`, and one bounded first-parent log
  read in `gitutil`.
- `nugit stats -reviewers [-months N] [-ref R] [-json]`. With no `-ref` it
  reads origin's default branch.
- A repo with no reverts gets a table of merged counts and a rate of zero.
  That is the answer B waits on.
