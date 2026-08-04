---
schema_version: 1
id: ADR-0023
type: decision
scope: global
status: proposed
created: 2026-08-01T00:00:00Z
relates_to:
  - constrains:trailers
  - constrains:config
  - constrains:significance
  - informs:ADR-0005
provenance:
  commit: seed
  citation: "feat/capture-nudge"
confidence: high
---

# ADR-0023 — Significance-aware capture nudge in the commit hook

## Context

The pilot repo exposed a capture-rate bottleneck: only 13 of 148 significant
commits (8.8%) carry a `learned:`/`decision:` trailer — yet the trailers that
DO exist are excellent, fully-formed lessons. The bottleneck is prompting, not
skill. `capture.commit_msg: warn` never prompts anyone: it only validates
trailer syntax when a block is already present, so the 91% of significant
commits with no block sail through in silence. Meanwhile nugit already
computes significance (`internal/significance`) for pr-render but never uses
it at commit time — the one moment the author still holds the why. ADR-0005
makes trailers the transient capture signal that `nugit distill` promotes to
durable files; a signal almost nobody emits feeds nothing.

## Decision

1. **A new `capture.commit_msg: nudge` level between `warn` and `block`.** In
   nudge mode the commit-msg hook keeps warn's validation behavior and
   additionally classifies the STAGED diff (`git diff --cached --numstat`,
   HEAD↔index — available because commit-msg runs after staging). If the
   change is significant per the existing thresholds (`significance.Classify`
   over the staged CodeDelta, `.nugit/**` excluded so committing knowledge is
   never nagged about capturing knowledge) and the message has no trailer
   block, the hook prints a short nudge quoting the classifier's reasons plus
   a copy-pasteable stub: a `learned:` skeleton and `keywords:` seeded from
   the touched C4 components (via `internal/mapping` when the working-tree
   model binds paths) or, for unmapped files, from path names.
2. **The nudge NEVER blocks.** Every nudge path exits 0, and every internal
   failure — unborn HEAD, not a git repo, unparsable model, git timeout, even
   a panic — degrades to silence, preserving the hook's existing
   never-block-on-error discipline.
3. **Bounded cost.** The nudge adds exactly one git call, hard-bounded by a
   100 ms timeout (`gitutil.NumstatCached`); staged diffs beyond 400 files
   skip the nudge entirely rather than slow the commit (pr-render still sees
   them); merge/`fixup!`/`squash!` subjects skip outright. The whole path
   stays within the ~150 ms commit-latency budget.
4. **Unknown config values keep falling back to `warn`,** exactly as today.
   `nudge` must be opted into explicitly; a typo never silently changes hook
   behavior, and `warn` remains the `nugit init` default.

## Rejected

- **Make `block` the default (force capture).** Pilot evidence shows quality
  is already high when capture happens; blocking would push the 91% into
  writing junk trailers to get past the gate, polluting the exact signal
  distill promotes — and it violates the never-break-commits discipline the
  hook was built on. `block` stays available for teams that opt in.
- **Nudge on every commit regardless of significance.** Habituation kills it:
  a prompt that fires on typo fixes trains authors to ignore it before the
  first significant commit arrives. Progressive disclosure is the entire
  point of the significance tier — a nudge that always fires carries no
  information. The trivial tier (≤2 files, ≤20 lines by default) commits are
  precisely the ones with no why worth capturing.
- **Nudge from a pre-commit hook instead.** pre-commit runs before the
  message exists, so it cannot know whether the author was about to write a
  trailer block; commit-msg is the only hook that sees both the staged diff
  and the message.
- **LLM-drafted stub content.** The keystone is deterministic and LLM-free
  (ADR-0006), and a network call inside a commit hook breaks both the latency
  bound and offline commits. The stub is a skeleton the author fills in.

## Consequences

- Adoption is a one-line config change (`warn` → `nudge`); the pilot can trial it
  without re-running `nugit init` or reinstalling the hook.
- Every nudge quotes the classifier's reasons, so a misfire is auditable the
  same way pr-render verdicts are (§13.6 transparency).
- At commit time only the code delta exists (no C4/knowledge deltas are
  computed), so the staged classification can reach at most the feature tier
  via file-count/churn/component-span signals — a deliberate conservative
  under-approximation: it can only under-nudge relative to pr-render, never
  invent significance pr-render would not see.
- New `internal/nudge` component and its edges are declared in
  `workspace.dsl`; the hook script installed by `nugit init` is unchanged (it
  already execs `nugit hook commit-msg`).
- Whether nudging actually moves the 8.8% capture rate is measurable in the
  pilot; if it does, a future ADR can consider making `nudge` the scaffold
  default.
