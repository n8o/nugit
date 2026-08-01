---
schema_version: 1
id: ADR-0018
type: decision
scope: global
status: proposed
created: 2026-08-01T00:00:00Z
relates_to:
  - constrains:distill
  - constrains:engine
  - constrains:render
provenance:
  commit: seed
  citation: "feat/pr-distill"
confidence: high
---

# ADR-0018 — PR-time distill: captured trailers become proposed lessons automatically

## Context

Capture works; promotion never happens. In the pilot repo, 38 of the last 600
commits carry high-quality `learned:` trailers (already keyworded per the
mandatory-field rule), but only ~4 ever became lesson files. `nugit distill`
exists and works — it is wired into no hook, no CI, and no habit, so trailers
are a write-only log. The worst cases are exactly the knowledge the store
exists for: the #1923 preview-freeze saga (an e85eb477 experiment → an
e3a31abd revert whose subject literally reads "the experiment answered its
question" → the a0124496/baaf9c36 dependency-tag bumps that fixed it) and the
#1917 three-fault production postmortem (79fe4df8) produced zero knowledge
objects. ADR-0005 already decrees that trailers are a capture *signal* to be
distilled into files within the PR, before squash-merge destroys them; ADR-0016
made distill's output a safe candidate lane (`status: proposed`, ratified
later). What is missing is the step that puts distill on the one path every PR
already walks: `nugit pr-render`.

## Decision

1. **`pr-render` computes the proposal set.** The engine runs the distiller's
   selection pass — a pure function over the range's parsed commits and the
   loaded store (`distill.Propose`) — for `mergeBase(base,head)..head` and
   attaches it to the Report. All three formats carry it: a "Proposed
   knowledge" section in the markdown comment, the check-run summary (which
   embeds the markdown), and a `Proposals` array in the structured JSON for
   reviewer agents. Rendering never writes files.
2. **`nugit distill` is the apply verb — no new flag.** It already writes
   exactly this set as `status: proposed` candidate files (ADR-0016), to be
   promoted via `nugit ratify`. The rendered section names the precise command
   (`nugit distill -base <base> -head <head>`); the selection pass is shared,
   so the view proposes exactly what apply writes.
3. **`-min-recur` defaults to 1** (was 2), for both the CLI and the PR-time
   pass. Within a single PR range a lesson almost never recurs — one commit,
   one insight — so the recurrence filter is what turned trailers into a
   write-only log. The candidate lane makes over-proposing cheap: ratification,
   not recurrence, is the quality gate. `-min-recur 2` restores the old filter.
4. **Dedupe is shared between view and apply, and gains a keyword rule.** A
   lesson candidate is suppressed when the store already carries its exact
   normalized insight text (existing behavior) or when a single existing lesson
   shares ≥2 keywords covering ≥ half of the candidate's keyword set.
   Suppressed candidates are counted in the section, never listed.
5. **The section is budget-conscious (ADR-0006).** At most 5 proposals render,
   two lines each; overflow and dedupe are one-line counts. No section at all
   when the range has nothing to propose.

## Rejected

- **A hook or CI job that auto-runs distill and commits the files** — a
  machine writing knowledge objects into a contributor's branch without a
  deliberate act inverts the candidate lane's ethos (ADR-0016: only a
  deliberate act promotes) and violates single-writer discipline (ADR-0011).
  The render section is the nudge; the author stays the writer.
- **LLM summarization of trailers at PR time** — the keystone is deterministic
  and LLM-free (ADR-0006); the trailer text is already the author's own
  distillation, so a model adds cost and non-reproducibility for no signal.
- **Rendering proposals as consistency findings (warn severity)** — a proposal
  is an offer, not a defect. Findings feed `-fail-on` gates and the eval
  precision corpus; a nudge dressed as a warning rots into noise and gets the
  check disabled — the disable-the-check failure mode ADR-0006 exists to avoid.
- **Keeping `-min-recur` at 2 for PR ranges** — the pilot evidence is direct:
  the recurrence filter reduced 38 quality trailers to ~4 lessons; within one
  PR a single excellent trailer is worth proposing.
- **Semantic/embedding dedupe** — non-deterministic and needs a model call on
  the render path; keyword overlap is explainable, cheap, and the keywords are
  already mandatory trailer fields.

## Consequences

- Every `pr-render` surfaces the PR's uncaptured knowledge while the trailers
  still exist (ADR-0005's timing), with apply one copy-pasteable command away;
  the output lands in the candidate lane, so no trust is minted (ADR-0016
  intact) and `decision-coverage` semantics are unchanged.
- Reviewer agents consuming the structured JSON see the proposal set and can
  run the apply step themselves.
- `engine` now depends on `distill`; the edge is declared in `workspace.dsl`.
- The keyword-overlap dedupe also governs distill writes: a near-duplicate
  lesson is skipped (counted, not minted) — slightly stronger idempotence.
- Follow-ups: a CI recipe that posts the section when trailers are present,
  a commit-msg hook nudge ("this trailer will be proposed at PR time"), and
  check-run annotations per proposal.
