---
schema_version: 1
id: ADR-0055
type: decision
scope: global
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:config
  - constrains:engine
  - constrains:editgate
  - constrains:cli
  - amends:ADR-0041
  - amends:ADR-0026
  - informs:ADR-0042
  - informs:ADR-0023
provenance:
  commit: seed
  citation: "nugit main at aab63ec, 2026-11-21: internal/engine/engine.go read .nugit/config.yml at opt.Head and cmd/nugit's pr-render read pr_render.fail_on from the checkout; Claude Code team, 'How the Claude Code team thinks about verification' (2026-11): 'If a change can modify the check that judges it, the check is broken'"
confidence: high
---

# ADR-0055 — A change cannot set the rules that judge it

## Context

Found 2026-11-21: `BuildReport` read `.nugit/config.yml` at `opt.Head`, and
`nugit pr-render` took its default `-fail-on` from the checkout, which in CI is
the PR's own tree. A PR could write `pr_render.fail_on: none`, or
`c4.mode: warn`, or `plan.mode: off`, and be judged by the policy it had just
written. Nothing in the render said so. ADR-0026 made an explicit `-fail-on`
downgrade visible, but compared it against config *at head*, so a PR that
lowered the config value itself showed no downgrade at all.

The local gates have the same hole. `capture.commit_msg: block` (ADR-0023) and
`context_gate: block` (ADR-0042) are read from the working tree, so an agent
that is denied an edit can switch the gate off in the file next to the one it
was editing.

The Claude Code team states the rule as: if a change can modify the check that
judges it, the check is broken. Their reviewer reads its instructions from the
base branch, never from the PR. nugit already does this for one question:
ADR-0041 reads id uniqueness from the target branch's tip because the head
cannot answer it. The gate policy is the same kind of question. It is not "what
did this PR change" but "what rules does this PR have to meet", and the PR
does not get to answer that one.

## Decision

1. **Config is read from the target, and head supplies only what describes
   its own tree.** At PR time the target is `-base` at its tip, read the way
   ADR-0041 reads it. The keys read from head are an allowlist:

   | read at head | why |
   |---|---|
   | `c4.dsl` | where the model lives in the reviewed tree. A PR may move it. |
   | `plan.scope` | which plans the view renders. It never changes a verdict. |
   | `retrieval.*` | bundle sizes for `context()`. pr-render does not read them. |
   | `schema_version` | the file's own format. |

   Every other key is **governed**: `pr_render.fail_on`, `c4.mode`,
   `plan.mode`, `contracts.mode`, `recurrence.*`, `significance.*`,
   `capture.commit_msg`, `context_gate`, `usage.log`, `narrative.*`, `org.*`
   and `peers`. Each one either sets how hard a check bites, decides whether
   it runs at all, decides how much of the PR is shown, or (for `narrative`)
   lets the PR start a network call in CI. The list is an allowlist of head
   keys, not a list of governed ones, so a key added later is governed until
   someone argues it out.

2. **A PR that changes a governed key gets an `enforcement-config` finding**
   (warn) naming each key and both values. The change does not take effect
   until it merges, so the PR that lowers `fail_on` is still judged by the old
   value, and the reviewer sees the change as a change instead of finding it
   in the diff.

3. **`-fail-on`'s default comes from the governing config.** The report
   carries the governing `fail_on`, and the CLI uses it for the exit code.
   ADR-0026's downgrade notice compares an explicit flag against that same
   value, and the wiring scan compares workflows against it.

4. **The local gates read governed keys from the remote default branch**
   (`refs/remotes/origin/HEAD`). The commit-msg hook (`capture.commit_msg`) and
   the pre-edit and session-start hooks (`context_gate`, `usage.log`) apply
   the same rule as pr-render. Where the clone records no remote default, or
   it cannot be read within the hook's deadline, they fall back to the
   checkout, as before. A local-only repo has no branch the change cannot
   modify, so there is nothing better to read.

5. **Where the target has no config, head governs.** That is the adoption PR
   (`nugit init` writes the ramp), and there is no merged policy for it to
   evade. A target config that does not parse also falls back to head rather
   than failing every PR until someone fixes main. The `enforcement-config`
   finding still reports what the PR sets, measured against the defaults.

## Rejected

- **Fail on a governed-key change.** Changing the policy is legitimate work.
  It needs a reviewer's eye, not a red build, and under `fail_on: warn` a warn
  already blocks.
- **A list of governed keys, with everything else read at head.** That fails
  open: the next knob someone adds would be PR-controlled until someone
  noticed. The head list has four entries and each has a reason.
- **Reading `c4.dsl` from the target.** A PR that moves the model would be
  checked against a path that no longer exists at head and report every edge
  as undeclared. The DSL's location is a fact about the tree under review.
- **Reading the local gates from the branch's upstream.** A pushed feature
  branch tracks itself, so its upstream is exactly the ref the change can
  modify. `origin/HEAD` is the branch it merges into for nearly every clone,
  and when it is missing the hook says nothing new rather than guessing.
- **Signing or pinning config.yml.** The base branch is already the protected
  thing, so reading from it is enough.
- **Governing the CI workflow too.** Under `pull_request`, GitHub runs the
  workflow from the PR's own tree, so a PR can pass `-fail-on none` or stop
  calling nugit. Only the repo can close that (required checks, or
  `pull_request_target` with its own risks). What nugit can do is report it,
  and the ADR-0026 wiring scan already does. It now compares the workflow
  against the target's `fail_on`, so a PR that weakens both its config and its
  workflow is flagged for both.

## Consequences

- A PR that lowers `fail_on` or turns a gate off is judged by the old rules and
  shows an `enforcement-config` warn. Once it merges, later PRs are judged by
  the new rules. That is where the change belongs.
- A PR that *raises* a gate does not get the stricter rules until it merges.
  To try a stricter mode first, run `nugit pr-render -fail-on <level>`
  locally.
- A local gate change in the checkout does not affect the hooks until it
  reaches `origin`'s default branch. Someone trying `context_gate: block` in a
  working tree will see warn behaviour until it lands. `nugit doctor` reports
  the governed keys where the checkout and the target disagree.
- `config` gains `Govern` and `GovernedChanges`, pure functions over two
  parsed configs. `gitutil` gains a bounded `ShowFileTimeout` for the hook
  path (LESSON-any-git-call-added-to-the-commit-path-must-carry-i). No new
  cross-package edge.
