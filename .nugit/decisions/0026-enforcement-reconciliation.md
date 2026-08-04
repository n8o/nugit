---
schema_version: 1
id: ADR-0026
type: decision
scope: global
status: accepted
created: 2026-08-01T00:00:00Z
relates_to:
  - constrains:engine
  - constrains:render
  - constrains:doctor
  - constrains:config
  - informs:ADR-0010
provenance:
  commit: seed
  citation: "feat/enforce-reconcile"
confidence: high
---

# ADR-0026 — Enforcement knobs must not silently cancel; wiring must not drift

## Context

On the pilot repo, `.nugit/config.yml` declares full enforcement — `c4.mode:
enforce` and `pr_render.fail_on: fail` — but the CI workflow invokes
`-fail-on none`, so BOTH knobs are inert and nothing announced it. Three new
undeclared containers (`apps/scte35_service`, `libs/scte104`,
`libs/anc_parser`) landed under nominally-enforcing config with nothing
firing. The `-fail-on none` had been set to work around a real blocker
(DETECT-5, partial component-level enforcement) that shipped in v0.3.0 weeks
earlier — the flag was simply never revisited, because nothing in the system
made the config/flag disagreement visible.

Separately, the wiring artifacts drifted on their own axes: the repo's nugit
SKILL.md still asserts "c4.mode: warn" (wrong since 2026-07-17 when the model
was ratified to enforce), and the install pins disagree across artifacts
(CLAUDE.md `@main`, SKILL.md `@main`, CI workflow `@v0.3.0`) — three sources
of truth, no reconciliation.

The failure class is the same in both cases: one artifact declares a policy,
another artifact quietly cancels or contradicts it, and no surface reports
the disagreement. Today `-fail-on` merely defaults FROM config
(cmd/nugit/main.go), so the engine cannot even tell "the user weakened the
run" apart from "config chose this level".

## Decision

1. **Downgrade visibility in pr-render.** The CLI records whether `-fail-on`
   was passed explicitly (vs. defaulted from config) and hands it to the
   engine (`engine.Options.FailOnFlag`). When the explicit flag is weaker
   than config's `pr_render.fail_on` under the strictness order
   `none < warn < fail` (`config.FailOnRank`), the report carries a
   `model.EnforcementDowngrade` and every output format leads with the same
   first-line notice:

   > enforcement downgraded by flag: config says X, running with Y

   Markdown opens with it as a blockquote, the check-run title is prefixed
   with it, and the structured JSON carries it as the first field
   (`Enforcement`, omitted entirely when no downgrade happened, so existing
   output stays byte-identical). The flag still wins — the exit code follows
   the flag, not config.

2. **Wiring coherence in doctor — advisory, never gating** (doctor is the
   setup pre-flight, ADR-0010). Doctor scans `.github/workflows/*.y*ml`,
   `CLAUDE.md`, and `.claude/skills/**/SKILL.md` with tolerant regexes (never
   a YAML parse; unreadable files are skipped, never an error) and warns
   when:
   - (a) a workflow's `-fail-on`/`fail-on:` value is weaker than config's
     `pr_render.fail_on`;
   - (b) multiple different version pins exist across the artifacts
     (`nugit@<ref>` / `n8o/nugit@<ref>` patterns);
   - (c) a skill file asserts a `c4.mode:` value that contradicts
     `config.yml`.

   All three checks are `Advisory` — they inform the human, never flip the
   doctor exit code.

## Rejected

- **Make config always win over the flag.** Legitimate weaker runs exist —
  local iteration, adoption ramps, temporarily gating around a known blocker
  (exactly the DETECT-5 situation). The failure on the pilot was not that the
  downgrade existed; it is that the downgrade was invisible and therefore
  immortal. Visibility, not coercion.
- **Hard-fail doctor on wiring drift.** Doctor is a pre-flight that must stay
  safe to run everywhere (ADR-0010); pins legitimately diverge mid-rollout
  and skill prose is documentation, not configuration. Gating on a regex
  scan of prose would make doctor flaky exactly where it must be trustworthy.
- **Auto-rewrite the workflow / pins from config.** Single-writer discipline
  (ADR-0011) — nugit does not mutate CI definitions or docs it does not own;
  and a scanner confident enough to warn is not confident enough to edit.

## Consequences

- A weakened run now announces itself on the first line of every render — a
  reviewer, a check-run reader, and a JSON-consuming agent all see the same
  sentence, so a "temporary" `-fail-on none` can no longer outlive its
  reason silently.
- The unset-flag path is unchanged: defaulting from config records nothing,
  and the structured JSON for undowngraded runs is byte-identical (golden
  test holds).
- Doctor gains three advisory lines; `AllOK` and the exit code are
  unaffected by them. The scan is regex-tolerant by design, so it can
  under-report (e.g. exotic YAML), but it can never break the pre-flight.
- The downgrade check compares the flag against config at the reviewed ref
  (head), like every other engine input — a PR that itself weakens
  `pr_render.fail_on` is judged against its own declared policy.
