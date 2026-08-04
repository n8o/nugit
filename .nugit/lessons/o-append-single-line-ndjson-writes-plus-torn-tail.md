---
schema_version: 1
id: LESSON-o-append-single-line-ndjson-writes-plus-torn-tail
type: lesson
scope: localmem
status: active
created: 2026-08-04T10:03:47Z
provenance:
  commit: b0eedfb6
confidence: medium
---

# Lesson — O_APPEND single-line NDJSON writes plus torn-tail-tolerant readers make a multi-…

**Trigger:** 28 worktrees of one repo each kept their own gitignored journal, so a note written by one agent was invisible to the other 27 and working memory went unused

**Insight:** O_APPEND single-line NDJSON writes plus torn-tail-tolerant readers make a multi-agent shared journal safe with zero coordination

**Rejected:** per-worktree server processes; MCP transport cwd sniffing; sharing canonical .nugit/ knowledge across worktrees (it is branch-versioned content)

**Keywords:** worktree, cwd, common-dir, mcp, localmem, usage, branch
