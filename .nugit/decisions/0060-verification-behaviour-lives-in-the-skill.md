---
schema_version: 1
id: ADR-0060
type: decision
scope: global
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:skills
  - informs:ADR-0055
  - informs:ADR-0056
  - informs:ADR-0057
  - informs:ADR-0058
  - informs:ADR-0006
provenance:
  commit: seed
  citation: "Claude Code team, 'How the Claude Code team thinks about verification' (2026-11): Claude uses the verify skill before committing; flaky tests may not be skipped, slept or retried until green, and a test with no root cause found is deleted with the reason written down; after three failed rounds on a check Claude stops and brings in a human; Claude runs the tests, build and linter before opening a pull request"
confidence: medium
---

# ADR-0060 — The verification behaviour lives in the skill; nugit only measures it

## Context

ADR-0055 to ADR-0058 give nugit the deterministic half of the Claude Code
team's verification practice. A change cannot set its own gate. Commits say
how they were verified, and a gate can hold them to it. Checklist items fire
on the diff. Rules are pruned on evidence.

The other half is behaviour: what the agent does when it is about to commit,
when a test fails, or when a test is flaky. None of it can be computed from
two git refs, and the keystone takes no model calls (ADR-0006). That half
lives in the text `nugit skill` installs. It reaches every repo that uses
nugit, sits next to the rules for `context()` and capture, and is checked
against the embedded copy by the repo's own tests.

## Decision

The `nugit` skill gains four rules and two pointers.

1. **Before committing, run the repo's verify skill** (one named `verify` or
   `verify-*`, nearest the changed code), after the last edit. With none,
   run the tests, build and linter, and tell the user a verify skill would
   make the check real. Then say how the commit was verified, writing
   `verified: none — <why>` when nothing did, and never claiming a kind the
   agent did not run. Under enforce, run the check or give the reason. Never
   re-try the same commit unchanged.
2. **Flaky tests are fixed or deleted with a reason, never skipped, slept or
   retried until green.** A deleted test carries `symptom:` and `decision:`
   trailers, so the deletion is on record and can be reviewed.
3. **Three failed fix rounds on a check, then a human.** The agent says
   which check, what it tried each round, what it saw, and what it now
   suspects.
4. **Cheap checks before pushing:** tests, build, linter and `nugit
   pr-render`, locally, before shared CI and review.
5. **Answer the checklist** when `context` or the PR view shows a `check:`
   (ADR-0057), and **turn a repeated miss into an item**: a bug seen in two or
   more PRs and readable from a diff gets a `diff_trigger:` and a `check:`.
6. **Do not lower a gate to get past it.** Governed config comes from the
   base (ADR-0055). An agent that thinks a gate is wrong tells the user.

## Rejected

- **Shipping a template `verify` skill with nugit.** The verify skill is the
  repo's own smoke test, and a template that runs nothing would be recorded
  as `e2e` evidence the first time an agent called it. Each repo writes its
  own. The skill tells the agent to ask for one when it is missing.
- **Enforcing the three-round rule in a hook.** "Round" and "the same check"
  can only be judged from the conversation. A hook would count tool calls
  and guess. The rule is stated where the agent reads it.
- **Detecting skipped or retried tests in pr-render.** Which `t.Skip`, retry
  loop or sleep is the flaky-test dodge, and which is legitimate, takes
  judgement. A repo that wants a mechanical check can write a checklist item
  for it (for example, `added: 't\.Skip\(|time\.Sleep\('` under its test
  paths), so the question gets asked on the diff without nugit ruling on
  it.

## Consequences

- Every repo that runs `nugit skill -install` gets the rules on its next
  install. Repos with a locally edited copy keep it until `-force` (the
  existing install rule).
- The rules now refer to behaviour nugit measures: `verified:` stamps,
  `would-block` counts and checklist matches. A repo can therefore see
  whether its agents follow them, instead of trusting the text.
