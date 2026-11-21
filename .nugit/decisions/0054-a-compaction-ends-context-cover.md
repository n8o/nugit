---
schema_version: 1
id: ADR-0054
type: decision
scope: global
status: accepted
created: 2026-11-20T00:00:00Z
relates_to:
  - constrains:editgate
  - constrains:usage
  - constrains:cli
  - amends:ADR-0042
  - informs:ADR-0048
provenance:
  commit: seed
  citation: "a large pilot repo, 2026-11-20: replaying its shared usage log against the compact_boundary events in its Claude Code transcripts (27 transcripts, 90 compactions in 17 sessions): 1,717 hook edits in 19 sessions, 15 of which compacted; 1,260 edits were recorded covered, 846 of them after a compaction, and 106 were covered only by a context() call made before the session's latest compaction. Prior art: Graphify (Graphify-Labs/graphify) resets its hook's nudge budget on SessionStart `compact`"
confidence: high
---

# ADR-0054 — A compaction ends a session's context cover

## Context

ADR-0042 counts an edit as covered when an earlier `context()` call is about
the same component or path and comes from the same Claude Code session, the
same agent-deck session or, failing both, the same branch within four hours.
"Earlier" has no lower bound inside a session. A call made at the start of a
long session covers every later edit in it.

That is the wrong question once the session's context window is compacted.
Compaction replaces the conversation with a summary, and the bundle the call
returned is gone from the window. A `/clear` drops it entirely. The model
editing the file no longer holds the decisions it was shown. The gate still
records the edit as covered and stays silent, which is exactly the edit it
exists to catch.

On a large pilot repo, replaying the shared usage log against the compaction
events in its Claude Code transcripts found:

- 1,717 edits in 19 sessions, 15 of which compacted.
- Of 1,260 edits recorded covered, 846 were made after a compaction.
- 106 of those were covered only by a call made before the session's latest
  compaction. That is 8% of covered edits counted with no bundle in the
  window.

Claude Code says when this happens. The SessionStart hook fires with
`source: compact` after a compaction and `source: clear` after a `/clear`.
nugit's SessionStart hook is already installed with exactly those matchers
(ADR-0048), and it was discarding its stdin.

## Decision

1. **A compact or clear writes a boundary record.** The SessionStart hook
   reads its stdin. When `source` is `compact` or `clear`, it appends
   `source: "boundary"` to the shared usage log with the session id (stdin,
   then `CLAUDE_CODE_SESSION_ID`), the agent-deck id, the branch and `boundary:
   compact|clear`. `startup` and `resume` write nothing: a new session has no
   context to lose, and a resumed one gets its conversation back. A boundary
   with neither id is not written, because nothing can match it. The hook
   writes nothing when `context_gate: off` or `usage.log: off`, as the gate
   itself does. Like every nugit hook, it can never break a session.
2. **Only calls after the latest boundary cover.** For an edit, the boundary
   is the latest one at or before it that belongs to the edit's Claude Code
   session or agent-deck session. Calls at or before it cover nothing, under
   all three attributions. The agent-deck match is what makes `/clear` work:
   the conversation gets a new session id while the MCP server keeps reporting
   the old one, and only the agent-deck id ties them together.
3. **Say why.** An edit that a pre-boundary call would have covered is
   recorded with `lost_to: compact|clear`. The warn message tells the agent its
   earlier call no longer counts, and why. An agent that remembers calling
   `context()` would otherwise read the warning as a bug.
4. **Count it.** `nugit stats` reports context resets and the edits that were
   uncovered only because of one. A boundary is never counted as a call.

## Rejected

- **Keep cover session-wide (status quo).** It measures that a call happened,
  not that its content informs the edit. ADR-0042 exists to measure the
  latter, and 106 edits on the pilot were counted with nothing in the window.
- **Expire cover by time instead.** A time limit guesses at what compaction
  reports exactly. It would expire cover in a short, intense session that never
  compacted, and keep it across a compaction an hour in.
- **Treat `startup` as a boundary too.** A new session has no earlier calls of
  its own, and ending branch-window cover at every startup would change
  coverage for sessions that never lost anything. ADR-0042 chose that window
  deliberately.
- **Write the boundary from the pre-edit hook by detecting compaction.** The
  pre-edit hook cannot see the transcript, and guessing from gaps in the log
  would be a heuristic. Claude Code already states it, once, in the hook built
  for it.
- **Reset only the session match.** The branch and agent-deck fallbacks would
  then quietly re-cover the edit with the same pre-compaction call. One
  boundary has to apply to every attribution.

## Consequences

- After a compaction, the first edit to each component warns until the agent
  calls `context()` again. That is the intended cost.
- Existing installs get this with no reinstall: the SessionStart matcher
  already includes `compact` and `clear`.
- Coverage figures from before this change overstate coverage in long
  sessions. Comparisons across the change should use `lost_to_reset` to tell
  the two effects apart.
