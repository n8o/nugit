---
schema_version: 1
id: LESSON-under-agent-fan-out-correctness-must-live-in-the-r
type: lesson
scope: mcp
status: active
created: 2026-08-04T10:03:47Z
provenance:
  commit: 2f3e6b49
confidence: medium
---

# Lesson — under agent fan-out, correctness must live in the request, not in launch-time wi…

**Trigger:** usage telemetry recorded the same stale branch across ten consecutive retrievals over ten days, while the agents making them were each editing a different worktree

**Insight:** under agent fan-out, correctness must live in the request, not in launch-time wiring; versioned knowledge stays per-worktree while unversioned derived state shares the common root

**Rejected:** one MCP server process per worktree (28 processes; project-level .mcp.json cannot be parameterized per worktree); transport-level cwd sniffing (not reliably provided)

**Keywords:** worktree, mcp, cwd, common-dir, working-memory, usage-log, branch-attribution
