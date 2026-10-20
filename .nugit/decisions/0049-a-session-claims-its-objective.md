---
schema_version: 1
id: ADR-0049
type: decision
scope: claim
status: proposed
created: 2026-10-19T00:00:00Z
relates_to:
  - constrains:claim
  - constrains:cli
  - constrains:agentcfg
  - amends:ADR-0048
  - informs:ADR-0042
  - informs:ADR-0025
provenance:
  commit: seed
  citation: "ADR-0048 phase 2; phase 1's own PR had to write `objective:` by hand on every commit, and its second commit nearly went out without it"
confidence: medium
---

# ADR-0049 — A session claims its objective, and the commit-msg hook names it

## Context

ADR-0048 frames a PR by the `objective:` its commits name. An agent that has
to remember the trailer on every commit will forget some, and each one it
forgets shows up as a file "outside the objective". Agents also start sessions
with no idea that an objective is expected. Nobody running the session will
type a command to fix either: the agent is the operator, and the reviewer only
reads the PR.

## Decision

**A claim is local session state.** `nugit objective claim <bead-id>` appends
one line to `.nugit-local/claims.ndjson` at the repo's common root: branch,
objective, time, and the Claude Code and agent-deck session ids. It is shared
by every worktree and ignored by git, like the working-memory journal
(ADR-0025). Each claim is one O_APPEND write, and the newest line for a branch
wins.

**One branch, one objective.** Claiming a different objective on a branch that
already has one is refused, with a pointer to a new session on a new branch.
`-force` replaces it. Re-claiming the same objective records the resumed
session.

**The commit-msg hook names the claimed objective.** When the branch has a
claim and the message names no objective, the hook appends `objective: <id>`,
joining a trailing trailer block if there is one and staying above git's
comment block. A message that names a different objective is left alone and
warned about. This runs whatever `capture.commit_msg` says, because naming
the objective is not capture. Gerrit's Change-Id hook is the precedent for a
commit-msg hook adding a line.

**A SessionStart hook tells each session where it stands.** `nugit hook
session-start` (on startup, resume, clear and compact) injects a short digest,
capped at 2,000 characters: the branch's objective and the rule to stay on it,
or how to claim one and the beads in progress. It is silent in a repo with no
`.nugit/` and can never fail a session. `nugit agent -install -hook` installs
it alongside the pre-edit gate, and `nugit doctor` reports it.

**With no claim, the branch's own commits answer.** The first `objective:` on
commits since the merge base with origin's default branch is used. The base is
read from `refs/remotes/origin/HEAD` and never guessed.

## Rejected

- **Store the claim in the plan store (a bead field).** The claim is about a
  session and a branch, not the task. Writing it into `.beads/` would put
  per-session churn into every PR's diff and conflict across agents.
- **Key the claim by session id.** A resumed or cleared session gets a new id,
  but the branch is the same work. The branch is what commits and PRs share.
- **Require the agent to type the trailer.** That is the failure this decision
  removes.
- **Block a commit with no objective.** The hook adds it when it can, and the
  PR view warns when it can't. Blocking would stop commits in repos and
  sessions that have not adopted objectives.

## Consequences

- `internal/claim`: the claim log, branch resolution, message stamping and the
  digest.
- The nugit skill gains a "claim one objective" section.
- Phase 3 (`nugit orphans`) reads the claim log: a claim whose session
  stopped while its bead is open is an orphan candidate.
