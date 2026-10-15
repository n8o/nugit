#!/usr/bin/env bash
# usage-window.sh <worktree-or-repo> <branch|*> <start-epoch> <end-epoch>
# Print one JSON object describing the nugit context() calls served to one
# eval run: its count, the sizes it delivered, and whether the delivered bundle
# is too large for Claude Code to hand to the agent.
#
# nugit writes its usage log to the git common root (.nugit/.cache/usage.jsonl
# under the main checkout, ADR-0025), so the log is read from there and
# filtered to the run's branch and time window. For a linked worktree that is
# the source repo's checkout; for run.sh's leak-free clone it is the clone
# itself (a clone is its own repo), and run.sh passes branch "*" (any branch:
# the clone is fresh per run, so every record in the window is this run's).
# Records from the pre-edit hook (source "hook", ADR-0042) are edits, not
# context() calls, and are counted separately as hook_edits.
#
# Output limit (https://code.claude.com/docs/en/mcp, "MCP output limits"):
#   - a warning above 10,000 tokens (fixed);
#   - results above MAX_MCP_OUTPUT_TOKENS (default 25,000) are saved to a file;
#   - for a tool that does not declare anthropic/maxResultSizeChars, a result
#     over 50,000 characters is saved to a file whatever its token count.
# In both cases the agent gets a message naming a file, not the bundle. nugit
# sizes bundles at runes/4 per token, so characters ≈ 4 × estimated_tokens.
# over_limit is true when either threshold is crossed; MAX_MCP_OUTPUT_TOKENS in
# the environment is honoured so a run made with a raised limit is judged by it.
set -euo pipefail
wt=$1 branch=$2 t0=$3 t1=$4
common=$(git -C "$wt" rev-parse --path-format=absolute --git-common-dir)
log="$(dirname "$common")/.nugit/.cache/usage.jsonl"
if [ ! -f "$log" ]; then
  jq -nc '{calls:0, hook_edits:0, est_tokens:null, budget_tokens:null, truncated:null, over_limit:null, reason:"no usage log at the common root"}'
  exit 0
fi
jq -sc --arg branch "$branch" --argjson t0 "$t0" --argjson t1 "$t1" \
  --argjson max_tokens "${MAX_MCP_OUTPUT_TOKENS:-25000}" --argjson max_chars 50000 '
  [ .[] | select($branch == "*" or .branch == $branch)
        | select((.time | sub("\\.[0-9]+"; "") | fromdateiso8601) as $t | $t >= $t0 and $t <= $t1) ] as $w
  | ($w | map(select(.source != "hook"))) as $c
  | ($c | map(.estimated_tokens) | max) as $est
  | ( if ($c | length) == 0 then null
      elif $est > $max_tokens then "estimated_tokens \($est) > MAX_MCP_OUTPUT_TOKENS \($max_tokens)"
      elif $est * 4 > $max_chars then "~\($est * 4) chars > \($max_chars)-char file-save threshold"
      else null end ) as $why
  | { calls: ($c | length),
      hook_edits: ($w | map(select(.source == "hook")) | length),
      est_tokens: $est,
      budget_tokens: ($c | map(.budget_tokens) | max),
      truncated: (if ($c | length) == 0 then null else ($c | any(.truncated)) end),
      over_limit: (if ($c | length) == 0 then null else ($why != null) end),
      reason: $why }' "$log"
