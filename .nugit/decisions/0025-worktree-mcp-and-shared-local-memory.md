---
schema_version: 1
id: ADR-0025
type: decision
scope: global
status: proposed
created: 2026-08-01T00:00:00Z
relates_to:
  - constrains:mcp
  - constrains:localmem
  - constrains:usage
  - informs:ADR-0009
  - informs:ADR-0013
provenance:
  commit: seed
  citation: "feat/mcp-worktree-roots"
confidence: high
---

# ADR-0025 — Worktree-correct MCP and shared local memory

## Context

The pilot repo runs **28 concurrent git worktrees with an agent in each**. That
topology breaks two of nugit's implicit assumptions:

1. **The MCP server bakes in one checkout at process start.** The
   project-level `.mcp.json` launches `nugit mcp` with `-C "."`, and
   `mcp.Serve(repoDir, in, out)` threads that single directory through every
   request. So every agent's `context` call — no matter which worktree it is
   editing — resolves the DSL, knowledge, and branch against the PRIMARY
   checkout. Telemetry confirms the damage: the `branch` field in
   `usage.jsonl` read the same stale value
   (`fix/moq-egress-activation-kills-readers`) across 10 consecutive events
   over 10 days while the primary checkout sat on `master`. The join between
   per-branch usage and per-branch outcomes that ADR-0013 exists for is
   silently broken under worktree fan-out.

2. **`.nugit-local/` working memory is per-worktree and gitignored**, so 28
   worktrees mean 28 amnesiac silos that die with their worktree. In practice
   they never even get born: zero `.nugit-local/` dirs exist across all 28 of the pilot's
   worktrees — `nugit remember` has 0% adoption, partly because a note jotted
   in one worktree is invisible everywhere else, so there is no compounding
   payoff to jotting it.

A third symptom of the same drift: the commit-msg hook wiring uses yet another
convention (`-C ".worktrees"`). Three surfaces, three ad-hoc answers to "which
checkout am I in?".

## Decision

1. **Per-request root resolution in the MCP server.** The `context` tool
   schema gains an optional `cwd` argument: the absolute directory the agent
   is working in. When present, the server resolves that directory's working
   tree per request (`gitutil.Repo.WorktreeRoot`) and serves the bundle from
   THAT checkout — its `workspace.dsl`, its `.nugit/` knowledge, its branch.
   Guard: the resolved tree must belong to the same repository as the server's
   start root (identical `git rev-parse --git-common-dir`); on any failure —
   absent, missing, not a git worktree, different repo — the server falls back
   to the process-start root, so clients that never send `cwd` get exactly the
   old behavior. `usage.jsonl` records the branch of the per-request root, not
   the process-start root.

2. **Shared local memory across worktrees.** `.nugit-local/` and
   `.nugit/.cache/usage.jsonl` resolve against the **git common root** — the
   parent of `git rev-parse --git-common-dir`, i.e. the main checkout — not
   the current worktree (`gitutil.Repo.CommonRoot`). All worktrees of one repo
   share ONE journal and ONE usage log. The boundary is principled, not
   convenient: **canonical knowledge (`.nugit/`) is versioned content and must
   track the branch being edited — per-worktree; derived per-machine state
   (working memory, usage telemetry) is unversioned and must not fragment —
   per-repo.** Concurrency story: both files are append-only NDJSON where each
   record is a single `O_APPEND` write of one line, and readers tolerate a
   torn trailing line — N concurrent agents never corrupt each other. Outside
   a git repo everything falls back to the given dir unchanged; in a
   single-worktree repo the resolved paths are byte-identical to before.

3. **No new wiring for `nugit agent`.** The generated `.mcp.json` stays
   `-C "."`: one server started from any checkout now serves every linked
   worktree, because correctness moved from launch-time wiring to the
   per-request `cwd`. The tool description instructs agents to always pass
   `cwd` when the repo uses worktrees.

## Rejected

- **One MCP server process per worktree.** 28 worktrees would mean 28
  long-lived processes to launch, wire, and reap — and it cannot even be
  expressed: Claude Code's project-level `.mcp.json` is shared by the project,
  not parameterized per worktree, so every instance would still launch with
  the same `-C`. Fixing the request, not the process count, is the only shape
  that scales with fan-out.
- **Client cwd sniffing via the MCP transport.** There is no reliable
  transport-level signal of the caller's directory: stdio MCP clients do not
  consistently provide roots/workspace metadata, and inheriting the server
  process's cwd is exactly the bug being fixed. An explicit, schema-documented
  argument is honest about who knows the truth (the agent) and degrades
  loudly-by-default (fall back to start root) instead of guessing.
- **Resolving canonical `.nugit/` knowledge against the common root too.**
  Superficially symmetric, actually wrong: knowledge is branch-versioned
  content — an agent on `feat/x` must read `feat/x`'s decisions, which is the
  very correctness this ADR restores. Only unversioned derived state shares.
- **Symlinking `.nugit-local/` from each worktree to the main checkout.**
  Needs per-worktree setup (the adoption gap again), breaks on Windows and on
  worktree creation paths nugit doesn't control, and leaves stale links when
  worktrees are pruned. Path resolution in code needs zero setup.

## Consequences

- Branch attribution in `usage.jsonl` is correct per request, restoring the
  ADR-0013 join between usage and outcomes under worktree fan-out; `nugit
  stats` run from any worktree sees the whole repo's calls (the log itself no
  longer fragments).
- Working memory compounds across agents: a lesson jotted in worktree 7 is
  retrievable by the agent in worktree 23 via `context()`. The scope/keyword
  filters in retrieval become load-bearing as the shared journal grows.
- Correctness now depends on agents passing `cwd`; the schema description
  carries that instruction. A client that omits it degrades to today's
  behavior, never worse.
- A cross-repo `cwd` is refused (silent fallback to the start root) — one
  repo's MCP server never serves another repo's knowledge.
- `git init --separate-git-dir` layouts fall back to per-worktree resolution
  (common-dir parent is not a checkout there) — documented, honest, rare.
- Existing pilot data needs no migration: the primary checkout IS the common
  root, so the historical `usage.jsonl` location is unchanged.
- The commit-msg hook's `-C ".worktrees"` convention is untouched here;
  unifying all checkout-resolution conventions on gitutil's
  `WorktreeRoot`/`CommonRoot` pair is follow-up.
