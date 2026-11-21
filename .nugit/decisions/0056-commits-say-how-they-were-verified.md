---
schema_version: 1
id: ADR-0056
type: decision
scope: global
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:verify
  - constrains:trailers
  - constrains:usage
  - constrains:agentcfg
  - constrains:config
  - constrains:engine
  - constrains:render
  - informs:ADR-0042
  - informs:ADR-0023
  - informs:ADR-0055
provenance:
  commit: seed
  citation: "Claude Code team, 'How the Claude Code team thinks about verification' (2026-11): commits stamped with how they were verified, including none; a commit gate with off/observe/enforce that releases after two blocks; Claude Code hooks docs (code.claude.com/docs/en/hooks): PostToolUse on Skill fires for Claude-invoked skills only, a typed slash command arrives as UserPromptSubmit with the prompt as typed, a Bash PostToolUse response carries stdout/stderr/interrupted and no exit code, failures go to PostToolUseFailure"
confidence: medium
---

# ADR-0056 — Commits say how they were verified, and a gate can hold them to it

## Context

A repo running many agents cannot read every diff, so it needs to know which
commits were checked and how. The Claude Code team stamps every commit with how
it was verified, and the stamp includes `none`, so unverified commits can be
queried. A commit gate can block a commit whose session ran neither `/verify`
nor `/code-review`. It has three modes: off, observe (log what would have been
blocked), and enforce. Under enforce a commit may give its reason instead, and
after two blocks in a session the next commit goes through, so a wrong block
cannot leave an agent stuck.

nugit already has each piece this needs. ADR-0042 records context() calls and
edits per session in the local usage log and joins them in a hook.
ADR-0023's commit-msg hook is the commit-time moment. ADR-0048 stamps a trailer
from session state. ADR-0055 makes a gate's mode come from the default branch.
What is missing is a record of the session's verification and a gate over it.

What a hook can see, from the Claude Code docs, not assumed:

- **PostToolUse on `Skill`** fires when Claude invokes a skill. A skill the
  user *types* as `/verify` does not go through the tool. It arrives only as
  **UserPromptSubmit**, with `prompt` as typed. UserPromptSubmit takes no
  matcher.
- **PostToolUse on `Agent`** fires when a subagent finishes, with
  `tool_input.subagent_type`.
- **PostToolUse on `Bash`** has `tool_response` `{stdout, stderr, interrupted}`
  and **no exit-code field**. A failed call goes to `PostToolUseFailure`.
- `CLAUDE_CODE_SESSION_ID` is set in Bash tool subprocesses, so a `git commit`
  run by the agent passes it to the commit-msg hook. It matches the hook
  `session_id` (ADR-0042).

## Decision

1. **A `verified:` trailer.** `verified: <kind>[, <kind>…] [— <note>]` with
   kinds `e2e`, `tests`, `review`, `build`, `lint`, `manual`, `none`. Parsed
   into `Trailer.Verified` and read with `trailers.ParseVerified`. Like
   `objective:` it is not capture, so a commit carrying only it is not a
   capture block.

2. **`nugit hook evidence`** records a verification that ran in a session as
   a `source: "evidence"` usage record (`evidence`, `via`, session ids):
   - `PostToolUse` `Skill`: a skill matching `verify.skills` is `e2e`, one
     matching `verify.reviews` is `review`;
   - `PostToolUse` `Agent`: a subagent type matching `verify.reviews` is
     `review`;
   - `PostToolUse` `Bash`: a command segment starting with one of
     `verify.tests` is `tests`, unless the response was interrupted or reports
     a non-zero exit;
   - `UserPromptSubmit`: a typed `/<name>` matching either list.

   A name matches when its last segment equals an entry, starts with it plus
   `-`, or ends with `-` plus it. The defaults are `verify` for skills,
   `review`/`reviewer` for reviews, and the common test runners for tests.
   Each repo writes its own verify skill. nugit only gives it a slot.

3. **The commit-msg hook stamps and gates.** It finds the session's evidence:
   records from the same Claude Code or agent-deck session, with no edit
   recorded for that session after them. A check that ran before the tree
   changed checked a different tree. Without the pre-edit hook there are no
   edit records, and any evidence in the session counts. The hook then:
   - **evidence:** stamps `verified: e2e, tests` (only when the message has no
     `verified:` line);
   - **a reason in the message** (`verified: none — docs-only change`, or any
     line with a note): lets it through as *explained*;
   - **otherwise:** stamps `verified: none`. Under **observe** it records
     `would-block` and prints nothing. Under **enforce** it blocks with a
     message naming both ways forward. After **two blocks** since the
     session's last commit, the next commit goes through stamped
     `verified: none — released after 2 blocked commits`.

   Every verdict is a `source: "commit"` usage record. A commit made with no
   session id at all is a person at a terminal. nugit has no evidence about
   how they checked their work, so it stamps nothing and gates nothing.

4. **`verify.gate: off | observe | enforce`, default `observe`.** The mode is
   governed (ADR-0055), read from the default branch. An unknown value falls
   back to `observe`, never to `enforce`.

5. **pr-render shows the range.** A *Verification* section counts the commits
   by kind and lists every unverified one with its reason. Under **enforce**,
   an `unverified-commits` warn names the commits with no line, a bare `none`,
   or a release. Under observe it never warns. A repo that has never stamped
   a commit sees nothing new.

6. **`nugit stats`** reports evidence by kind and the gate's verdicts:
   verified, explained, would-block, blocked, released. `nugit doctor` reports
   whether the evidence hook is installed, and
   `nugit agent -install -hook` installs it on PostToolUse
   (`Skill|Agent|Bash`) and UserPromptSubmit.

## Rejected

- **Enforce by default.** No adopting repo has a verify skill yet, so every
  agent commit would block. Under observe a repo learns its false-positive
  rate from `would-block` before turning the gate on. ADR-0042 followed the
  same path, for the same reason.
- **Trust a hand-written `verified: tests` with no evidence and no note.**
  It is the one claim the gate exists to check. Writing a note (`verified:
  tests — ran in CI terminal`) is enough, and it leaves a sentence a reviewer
  can read.
- **Count any evidence in the session, however old.** A review of the tree
  before the last ten edits passed a different commit. The edit records are
  already in the log, so the freshness rule costs one pass over them.
- **Gate commits made outside an agent session.** nugit would be guessing
  how a person checked their work. A person can write the line, and the PR
  view lists an unstamped commit either way.
- **Listen on PostToolUseFailure to record failed runs.** A failed test run
  is not verification. Recording it would only matter for a "tried and
  failed" verdict, and the stop-after-three-rounds rule lives in the skill
  text, not in a gate.
- **Decide the evidence by reading the transcript.** That means parsing a
  format nugit does not own, on the commit path. The hooks are the
  documented channel.

## Consequences

- New `internal/verify` component. `cli -> verify`, `engine -> verify`, and
  `verify -> {config, gitutil, trailers, usage, model}` are declared in
  `workspace.dsl`.
- The usage log gains evidence and commit records. `usage.Record.IsCall`
  keeps them out of context-call counts and coverage matching.
- `verified: none` will appear on agent commits in a repo that has the
  commit-msg hook but not the evidence hook. That is accurate, since no
  verification was recorded, and doctor names the fix.
- Matching skill names is a heuristic. A repo whose verify skill is called
  something else sets `verify.skills`.
- PostToolUse is documented as the success event. If a non-zero Bash exit
  were ever delivered there, only an "Exit code N" response text would catch
  it. The `via` field names the command, so a suspicious record can be
  audited.
