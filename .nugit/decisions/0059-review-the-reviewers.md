---
schema_version: 1
id: ADR-0059
type: decision
scope: global
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - informs:ADR-0044
  - informs:ADR-0057
  - informs:ADR-0058
  - informs:ADR-0056
  - informs:ADR-0006
provenance:
  commit: seed
  citation: "Claude Code team, 'How the Claude Code team thinks about verification' (2026-11): track reverted merges, maintainer-rejected changes and a size-matched clean subset; evaluate on past incidents and reverts with a one-sentence answer key, merged-and-stable changes as a control, a tuning set split from a fixed test set, and recall measured separately for raw findings, post-verification and final review"
confidence: low
---

# ADR-0059 — Review the reviewers: revert rates per period, and recall per stage over replay tasks

> **Design only. Nothing here is built.** This record is a proposal for the
> owner's call (VERIFY-5). It says what nugit could measure deterministically,
> what it would cost, and what would stay outside the keystone.

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

## Proposal

### A. Outcomes per period (`nugit stats -reviewers`): cheap

From the default branch's first-parent history, per calendar month:

- **merged changes:** first-parent commits.
- **reverts:** commits whose body says `This reverts commit <sha>`, each
  joined to the change it reverted, with that change's age when reverted.
- **revert rate:** reverts over merged changes, and the reverted changes'
  size (files, churn) next to the median.
- **a size-matched clean set:** for each reverted change, the nearest-size
  merged change from the same month that no revert or `fix:` follow-up
  touched within 30 days. This set is the control for B.

Deterministic, git-only, bounded like every history scan (window plus
`maxCount`). Estimate: about 200 lines plus tests, in the shape of
`hygiene`. **"Rejected by a maintainer before merging" is GitHub data**, so
it needs the network. It stays out of the keystone. A repo's CI can feed it
with `gh`, or the skill can ask for it.

### B. Recall per stage over replay tasks (`nugit eval stages`): moderate

Two labelled sets, both built from history:

- **incidents:** the changes A identified as reverted. The answer key is the
  revert's own message, so a person writes it once and nugit never
  generates it.
- **controls:** A's size-matched clean changes.

For each example, rebuild the state at its base (ADR-0044's leakage rule:
store, model and config read at base) and record what each stage did:

| stage | what it did on the example |
|---|---|
| in scope | records `retrieval.ScopedDecisions` admits for the changed files at base |
| served | of those, what context() would have served after ADR-0043's relevance cap |
| asked | checklist items the change's diff triggers (ADR-0057) |
| flagged | pr-render findings on the range, by check and severity |

Per stage, report the **flag rate on incidents against controls**: the share
of examples where the stage surfaced anything relevant. For replay tasks
with oracle keys, report the **oracle-key recall at "in scope" and at
"served"**. The gap between those two is exactly "found it, then lost it":
the relevance cap dropping a decision the work needed.

Examples split into **tune / test** by a hash of their id, so the split is
stable and nobody picks it. Thresholds and caps are tuned on *tune* only.

What stays out of the keystone: deciding whether a finding *correctly
describes* the bug in an answer key needs judgement. A repo that wants it
runs a model over B's JSON output in its own harness (`evals/agent-ab` is
the precedent). nugit's numbers are the part that can be computed.

Estimate: about 500 lines. The main cost is a context() that reads at a ref.
Retrieval reads the working tree today, and the "served" stage needs its
caps applied to a historical store. `ScopedDecisions` is the scoping half of
that already. The ranking and cap half would need the same treatment.

### C. Severity of what is caught: no change

The article separates must-fix, should-fix and nit, and code review tags
pre-existing problems. nugit's findings already carry fail and warn, and
ADR-0041 and the SIGNAL-1 work made PR-view findings base-relative, which is
the pre-existing rule. Nothing to add.

## Owner's call

1. **Build A?** It is cheap and stands alone. Revert rate per month is the
   number that says whether the other stages are worth tuning.
2. **Build B, and is a ref-reading context() worth it?** Without one, B can
   report "in scope", "asked" and "flagged", but not "served". "Served" is
   the stage that would show the relevance cap losing a needed decision.
3. **Where should GitHub-side signals live** (rejected PRs, review comments
   as answer keys)? The proposal keeps them out of the keystone. The other
   option is an opt-in `gh` read in CI, like `narrative`.

## Rejected (for the proposal)

- **One end-to-end score.** It cannot tell a stage that never finds a
  problem from one that finds it and then drops it.
- **Generating answer keys with a model.** The key is what grades the
  stages, and a model-written key grades the model. The revert message is
  already written by the person who reverted.
- **Grading on the hand-built `internal/eval` corpus alone.** It tests that
  the checks do what their authors meant. Only the repo's own reverts test
  whether that was what mattered.
