---
schema_version: 1
id: ADR-0042
type: decision
scope: global
status: proposed
created: 2026-10-14T00:00:00Z
relates_to:
  - constrains:usage
  - constrains:editgate
  - constrains:agentcfg
  - constrains:doctor
  - constrains:config
  - amends:ADR-0013
  - informs:ADR-0023
  - informs:ADR-0025
provenance:
  commit: seed
  citation: "a pilot repo, 2026-10: ~30 logged context() calls against 1,000+ merged PRs over three months, under agent instructions that say to call context before every non-trivial edit; Claude Code docs: env-vars (CLAUDE_CODE_SESSION_ID), hooks (PreToolUse input, additionalContext, permissionDecision)"
confidence: high
---

# ADR-0042 — Measure context() coverage at edit time; warn first, block by opt-in

## Context

ADR-0013 made `context()` calls observable, and the pilot that adopted it shows
the question it cannot answer. Its usage log holds about **30 calls against more
than 1,000 merged PRs** over three months, while that repo's agent instructions
say to call `context` before every non-trivial edit. The log proves the rule is
not followed. It cannot say *where*: a record is tied to no session, so no call
can be joined to the work it served, and an edit made with no call before it
leaves no trace at all. The denominator, edits, is missing, so coverage has no
number to report and nothing to improve against.

A prose instruction is the weakest form of a rule. nugit's own discipline is to
turn one into a measurement first and an enforcement point second (the commit
nudge in ADR-0023 is the capture-side precedent). The edit-time moment exists:
Claude Code runs a **PreToolUse hook** before every Edit/Write and hands it the
tool input on stdin.

What a process can learn about its session, from the Claude Code docs, not
assumed:

- Hook stdin carries `session_id`, `cwd`, `tool_name` and `tool_input`. Edit and
  Write name the target `file_path`, which is always absolute
  (code.claude.com/docs/en/hooks).
- `CLAUDE_CODE_SESSION_ID` is set "in Bash and PowerShell tool subprocesses,
  hook command subprocesses, and stdio MCP server subprocesses". For Bash and
  hooks it matches the hook's `session_id` and **is updated on `/clear`**; "an MCP
  server subprocess **retains the ID it was spawned with**"
  (code.claude.com/docs/en/env-vars).
- Nothing at the MCP protocol level (no `_meta`, no header) carries the session.
- A PreToolUse hook can return `hookSpecificOutput.additionalContext`, which is
  placed next to the tool result, or `permissionDecision: "deny"` with a
  `permissionDecisionReason` that is shown to Claude. `"allow"` also **skips the
  user's permission prompt**.

## Decision

1. **Every usage record carries its session, best-effort.** `usage.Record`
   gains `session` (from `CLAUDE_CODE_SESSION_ID`) and `agent_session` (from
   `AGENTDECK_INSTANCE_ID`, agent-deck's per-session id), both `omitempty`.
   Old lines parse unchanged, and a client that exports neither writes
   byte-identical records. Hook records add `tool`, `covered` (a pointer, so
   `false` is kept), `covered_by` and `gate`.
2. **`nugit hook pre-edit` is the PreToolUse entrypoint.** For Edit, Write,
   MultiEdit and NotebookEdit (`notebook_path` is accepted too), it resolves the
   file to its worktree and its component through `retrieval.ResolveComponent`,
   the same resolution `context()` uses. It then looks for an earlier
   context() call that covers the edit. A call covers it when it was for the same
   component, or for a path that is the file or one of its ancestors (relative or
   absolute), **and** when either:
   - it came from the same Claude Code session or the same agent-deck session, or
   - failing both, it was on the same branch within **4 hours**.
   `covered_by` records which match won. A session match beats a branch match.
3. **Every edit seen is recorded** as `source: "hook"`, covered or not, in the
   one shared log (ADR-0025).
4. **`context_gate: warn | block | off` in config.yml, default `warn`.**
   - **warn:** the hook returns only `additionalContext`, naming the exact
     `context` call to make. It makes **no** permission decision, because
     `"allow"` would also skip the user's prompt, and the gate has no business
     approving an edit.
   - **block:** returns `permissionDecision: "deny"` with that message as the
     reason.
   - **off:** the hook records nothing.
   The gate also stands down when `usage.log: off`, because with no context
   records every edit would look uncovered. An unknown value falls back to
   `warn`.
5. **The hook can never break an edit.** Unparsable stdin, a non-edit tool, a
   file outside any checkout, a malformed config, or a panic all mean exit 0
   with nothing printed, which Claude Code treats as no opinion.
6. **`nugit stats`** adds edit coverage: edits seen, edits preceded by
   context(), a percentage, which match covered them, and a per-session
   breakdown (calls, edits, covered) once any record carries a session.
   Context-call counts exclude hook records.
7. **Installation is opt-in.** `nugit agent -client claude-code -install -hook`
   *merges* the hook into `.claude/settings.json`. It keeps every existing key,
   hook and the key order, does nothing if already installed, and refuses to
   rewrite a file it cannot parse. `nugit doctor` reports whether the hook is
   installed (advisory).

## Rejected

- **Block by default.** The pilot's rate is about 3%. Making block the default
  would deny nearly every edit on day one. Agents would learn to call `context`
  as a ritual to get past the gate, which pollutes the very measurement this
  exists to take. The lesson from ADR-0023 applies: a gate that fires on
  everything trains people to defeat it. Measure under `warn`, then promote a
  repo to `block` once its coverage shows the habit exists.
- **Warn with `permissionDecision: "allow"` plus a reason.** It is the obvious
  shape, and it silently auto-approves edits the user's permission mode would
  have prompted for. A measurement tool must not widen permissions.
  `additionalContext` alone is the documented context-only channel.
- **Attribute sessions only from the MCP server's environment.** The docs say
  an MCP server keeps its spawn-time id across `/clear`, so after a clear the
  server's calls carry a stale id while the hook carries the new one. Hook stdin
  is the authoritative id for edits. The agent-deck id, which survives `/clear`,
  and the branch fallback cover the gap. The record states which match covered
  each edit, so a weak match is never mistaken for a strong one.
- **A SessionStart hook that writes `session_id` into `CLAUDE_ENV_FILE`.** It is
  documented, but it reaches Bash only, not MCP servers, and
  `CLAUDE_CODE_SESSION_ID` already reaches Bash directly. It would be a second
  hook to install for no new information.
- **Branch-only matching.** Worktree fan-out makes branches nearly per-session,
  but not always. Two sessions on one branch would cover each other's edits
  indefinitely. The window bounds this, and session ids are tried first.
- **Overwrite `.claude/settings.json` the way `.mcp.json` is written.** A
  project's settings carry permissions, env and other hooks, and clobbering them
  is a security regression, not an install step.

## Consequences

- Coverage becomes a number per repo and per session. That number is the
  evidence a repo needs to move from `warn` to `block`, or to conclude the
  instruction is wrong.
- The log gains one line per edit. That is still local and gitignored, but it
  grows faster than the context-call log. `Read` streams it, and the hook reads
  it once per edit.
- Coverage is a heuristic. A component-level call covers every file in that
  component, and the branch fallback can credit another session's call within
  the window. Both are visible in `covered_by`.
- `MultiEdit` and the NotebookEdit path field are not in the published tools
  reference. Both are matched defensively, and a tool that never fires is
  harmless.
- New `internal/editgate` component; `cli -> editgate`, `editgate -> {config,
  gitutil, retrieval, usage}` and `doctor -> agentcfg` are declared in
  `workspace.dsl`.
