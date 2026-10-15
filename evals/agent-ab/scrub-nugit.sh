#!/usr/bin/env bash
# Remove every trace of nugit from a throwaway worktree so the "without" arm is
# a clean control. Worktree-local only: run.sh restores these paths from git
# before scoring, so nothing here is ever committed. Usage: scrub-nugit.sh <worktree>
set -euo pipefail
WT=${1:?usage: scrub-nugit.sh <worktree>}
cd "$WT"

# Project-level MCP wiring and nugit-named skills/commands/agents.
rm -f .mcp.json
if [ -d .claude ]; then
  find .claude -name '*nugit*' -prune -exec rm -rf {} +
fi

# Instruction files: drop every top-level block (paragraph, heading, or list
# item with its indented continuation and fenced code) that mentions nugit.
strip_blocks() {
  local f=$1 tmp
  tmp=$(mktemp)
  awk '
    function flush() { if (blk != "" && tolower(blk) !~ /nugit/) printf "%s", blk; blk = "" }
    {
      fence_line = ($0 ~ /^[ \t]*(```|~~~)/)
      start = !infence && $0 ~ /^[^ \t]/ && (prev_blank || $0 ~ /^([0-9]+\.|[-*]) / || $0 ~ /^#/)
      if (start) flush()
      blk = blk $0 "\n"
      if (fence_line) infence = !infence
      prev_blank = ($0 ~ /^[ \t]*$/)
    }
    END { flush() }
  ' "$f" > "$tmp"
  cat "$tmp" > "$f"
  rm -f "$tmp"
}
for f in CLAUDE.md AGENTS.md GEMINI.md .claude/CLAUDE.md .claude/rules/*.md; do
  [ -f "$f" ] && grep -qi nugit "$f" && strip_blocks "$f"
done

# The memory store itself: hidden so the agent cannot read the ADRs as plain files.
rm -rf .nugit
true
