---
schema_version: 1
id: ADR-0019
type: decision
scope: global
status: proposed
created: 2026-08-01T00:00:00Z
relates_to:
  - constrains:consistency
  - constrains:reinforce
  - constrains:knowledge
  - constrains:gitutil
  - constrains:retrieval
  - elaborates:ADR-0003
  - elaborates:ADR-0015
provenance:
  commit: seed
  citation: feat/recurrence-reinforce
confidence: high
---

# ADR-0019 — Recurrence detection and lesson reinforcement

## Context

Recurrence is the strongest capture signal the pilot data shows, and nugit
ignores it. Four concrete failures from the pilot store:

- A lesson about the registry's tag GC reaping non-protected semver tags
  existed (created 2026-06-24), and the same failure class recurred **15 days
  later** against a *different* tag family — pre-check base images reaped
  mid-run (pilot commit d77f90d5). The correct generalization ("every tag
  family a workflow pushes-then-pulls must be in the keep-tags allowlist")
  survives only in an orphan commit trailer; the ratified lesson still names
  only semver, so retrieval keeps serving the too-narrow rule.
- Operator pool re-staging oscillated across five commits in ~3 weeks
  (a52396f8, 31da37fd, a29e9543, 2907418f, c6d158bc) with no ADR and no
  lesson — pure fix-churn, zero durable knowledge.
- PVC sizing repeated after four months (afb55805 → 757bffe0).
- `media-function-format-defaults-leak` was only written after the SECOND
  incident.

Two distinct gaps: (1) nothing *notices* that the same component keeps being
fixed while the store stays silent; (2) when knowledge exists but proved too
narrow, there is no sanctioned way to widen it — ADR-0003 forbids editing a
ratified record, supersession semantically means "wrong" (ADR-0016), and
`amends:` means "partially overridden" (ADR-0015). Right-but-too-narrow fits
none of those relations.

## Decision

1. **A `recurrence` warn-severity consistency check** in
   `internal/consistency`, wired into pr-render. For each component the PR
   touches: one bounded `git log --no-merges --since=<window>` at head over
   the component's path globs (`:(top,glob)` pathspecs, `--max-count` capped —
   O(window), never O(history)). Fix-typed commits are conventional
   `fix(...)`/`revert(...)` subjects (plus git's `Revert "…"`). If
   `min_fixes` (default 3) or more accumulate inside `window_days` (default
   90) **with no knowledge delta in that window** — no scanned commit carries
   a `learned:`/`decision:` trailer and no live object governing the
   component was created in the window — warn, listing the commits and
   pointing at capture (`nugit distill`) or, when governing knowledge already
   exists, at reinforcement. Warn only, and never a significance signal:
   history outside `(base, head]` must nudge, never block or reclassify an
   unrelated PR. Knobs: `recurrence.mode: warn|off`, `window_days`,
   `min_fixes` in config.yml.

2. **Reinforcement is a new appended-only object, never a mutation.**
   `nugit reinforce <id> -text "…" -keywords a,b` mints a small lesson-typed
   record `<ID>-R<n>` beside the target with
   `relates_to: [reinforces:<id>]`, scope inherited from the target, and the
   widened keywords in its body. It lands `status: proposed` by default
   (candidate lane, ADR-0016; `-status ratified` escape hatch mirrors
   distill). Because retrieval keyword-matches an object's own body and then
   follows `relates_to` one hop, the reinforcement matches the *new* failure
   vocabulary and pulls the reinforced target into the bundle — widened
   retrieval with the target's bytes untouched.

3. **`ReinforcedBy` is derived, not authored** — the loader computes it from
   reverse `reinforces:` edges, an exact mirror of `AmendedBy` (ADR-0015),
   and retrieval annotates items with it. Reinforcement is purely additive:
   the target keeps its own status (unlike supersession) and no part of it is
   overridden (unlike `amends:`) — it is recurrence evidence plus widened
   applicability.

## Rejected

- **Append a dated `## Reinforcement — <date>` section + widened keywords
  into the target file.** Mutates a record whose immutability attaches at
  ratification (ADR-0003 as amended by ADR-0016), recreates the same-file
  concurrent-write conflicts the supersede graph exists to avoid, and is the
  same shape ADR-0015 already rejected as "amendments log appended to the old
  ADR". `nugit ratify`'s one-line edit stays the *single* permitted in-place
  mutation.
- **Supersede the target with a widened rewrite.** Superseded means
  known-wrong (it drops the object from live retrieval and poisons
  stale-knowledge); a recurrence proves the lesson right-but-too-narrow.
  Killing the original also breaks inbound references for what is an
  addition.
- **Fail severity, or recurrence as a significance signal.** The evidence
  lives outside the PR under review; blocking or reclassifying PR N because
  of commits 1..N-1 punishes the wrong change. Warn is the capture-nudge lane
  (decision-coverage precedent).
- **LLM-judged "same failure class" clustering.** The keystone is
  deterministic and LLM-free (ADR-0006). Conventional-commit type × component
  path is the deterministic proxy; auditable-but-coarse beats
  smart-but-unexplainable.
- **Count only fixes since the last capture (sharper trigger).** Better
  recall on the "lesson existed, recurrence continued" case, but more state
  and edge cases; the simple no-delta-in-window rule ships first
  (thin-keystone, ADR-0004) and the sharpening remains open as a follow-up.

## Consequences

- First check that reads history *outside* `(base, head]`. Still git-only and
  deterministic given (repo, clock); the clock dependence is bounded and
  warn-severity. Shallow CI clones (`fetch-depth: 1`) silently under-count —
  the Action docs should recommend `fetch-depth: 0` (follow-up).
- Thresholds are tuned to the oscillation case (5 fixes in ~3 weeks fires;
  the 4-month PVC recurrence is outside a 90-day window by design — teams
  that want it widen `window_days`).
- A capture landing mid-window suppresses the warn for the rest of the
  window, even if fixes continue — the 15-days-later pilot case is addressed
  by the reinforcement verb, not the check (see the rejected sharper
  trigger).
- Reinforcements are ordinary lesson objects: doctor, obsidian, notion, and
  evidence tiers handle them with zero schema changes (`reinforces:` is an
  edge like `amends:`); a reinforcement of a component-scoped target also
  counts as fresh capture for that component.
- pr-render's markdown annotation of `ReinforcedBy` (alongside amended-by) is
  follow-up; retrieval annotates now.
