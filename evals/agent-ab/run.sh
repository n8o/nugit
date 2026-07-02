#!/usr/bin/env bash
# agent-ab runner: same tasks, two arms (with/without the nugit MCP tool),
# scored deterministically by `nugit pr-render -format json`. See README.md.
set -euo pipefail

TASKS="" REPO="" RUNS=1 OUT="results" JUDGE=0 KEEP=0
while getopts "t:r:n:o:jk" f; do
  case $f in
    t) TASKS=$OPTARG ;;
    r) REPO=$OPTARG ;;
    n) RUNS=$OPTARG ;;
    o) OUT=$OPTARG ;;
    j) JUDGE=1 ;;
    k) KEEP=1 ;;
    *) exit 2 ;;
  esac
done
[ -n "$TASKS" ] && [ -n "$REPO" ] || { echo "usage: $0 -t tasks.json -r repo [-n runs] [-o outdir] [-j] [-k]" >&2; exit 2; }
for bin in claude nugit jq; do command -v "$bin" >/dev/null || { echo "missing: $bin" >&2; exit 2; }; done

mkdir -p "$OUT"
RESULTS="$OUT/results.jsonl"
NUDGE="Before editing any file, call the nugit context tool (server nugit) with that file's path and your task. In-scope decisions list rejected alternatives; do not re-propose them."
MCPCFG="$OUT/nugit-mcp.json"
printf '{"mcpServers":{"nugit":{"command":"nugit","args":["mcp"]}}}\n' > "$MCPCFG"

run_one() { # task_json arm run_idx
  local task=$1 arm=$2 idx=$3
  local id prompt path base verify
  id=$(jq -r .id <<<"$task"); prompt=$(jq -r .prompt <<<"$task")
  path=$(jq -r .path <<<"$task"); base=$(jq -r '.base // "origin/master"' <<<"$task")
  verify=$(jq -r '.verify // ""' <<<"$task")

  local wt="$OUT/wt-$id-$arm-$idx" branch="eval/$id-$arm-$idx"
  rm -rf "$wt"; git -C "$REPO" worktree remove --force "$wt" 2>/dev/null || true
  git -C "$REPO" branch -D "$branch" 2>/dev/null || true
  git -C "$REPO" worktree add "$wt" -b "$branch" "$base" >/dev/null

  local -a flags=(-p --output-format json --permission-mode acceptEdits)
  [ "$arm" = with ] && flags+=(--mcp-config "$(cd "$(dirname "$MCPCFG")" && pwd)/$(basename "$MCPCFG")" --append-system-prompt "$NUDGE")

  echo ">> $id / $arm / run $idx" >&2
  local agent_json
  agent_json=$( (cd "$wt" && claude "${flags[@]}" "$prompt") 2>"$OUT/$id-$arm-$idx.stderr" || echo '{}')
  printf '%s' "$agent_json" > "$OUT/$id-$arm-$idx.agent.json"

  # Commit whatever the agent left uncommitted so pr-render (which reads the
  # reviewed ref, not the working tree) can score it.
  git -C "$wt" add -A >/dev/null
  local committed=true
  git -C "$wt" diff --cached --quiet && git -C "$wt" diff "$base"..HEAD --quiet 2>/dev/null && committed=false
  git -C "$wt" commit -q -m "eval: $id ($arm run $idx)" --allow-empty >/dev/null

  local render fail warn
  render=$(nugit pr-render -C "$wt" -base "$base" -head HEAD -format json -fail-on none 2>/dev/null || echo '{}')
  printf '%s' "$render" > "$OUT/$id-$arm-$idx.render.json"
  fail=$(jq '[.Findings[]? | select(.Severity=="fail")] | length' <<<"$render")
  warn=$(jq '[.Findings[]? | select(.Severity=="warn")] | length' <<<"$render")

  local calls=0
  [ -f "$wt/.nugit/.cache/usage.jsonl" ] && calls=$(wc -l < "$wt/.nugit/.cache/usage.jsonl" | tr -d ' ')

  local verified=null
  if [ -n "$verify" ]; then
    if (cd "$wt" && bash -c "$verify" >/dev/null 2>&1); then verified=true; else verified=false; fi
  fi

  local reproposed=null
  if [ "$JUDGE" = 1 ]; then
    local rejected diff judge_prompt
    rejected=$(nugit context -C "$wt" -path "$path" -format json 2>/dev/null | jq -r '[.decisions[]?.rejected // empty] | join("\n---\n")')
    diff=$(git -C "$wt" diff "$base"..HEAD | head -c 30000)
    if [ -n "$rejected" ] && [ "$committed" = true ]; then
      judge_prompt=$(printf 'You are grading a code change. REJECTED ALTERNATIVES from the project'\''s ADRs:\n%s\n\nDIFF:\n%s\n\nDoes the diff implement any rejected alternative? Answer with exactly one word: yes or no.' "$rejected" "$diff")
      if claude -p --output-format json "$judge_prompt" 2>/dev/null | jq -r '.result // ""' | grep -qi '^yes'; then
        reproposed=true
      else
        reproposed=false
      fi
    fi
  fi

  jq -nc --arg id "$id" --arg arm "$arm" --argjson idx "$idx" \
    --argjson fail "$fail" --argjson warn "$warn" --argjson calls "$calls" \
    --argjson committed "$committed" --argjson verified "$verified" --argjson reproposed "$reproposed" \
    --argjson agent "$(jq '{cost_usd: (.total_cost_usd // null), num_turns: (.num_turns // null), duration_ms: (.duration_ms // null)}' <<<"$agent_json")" \
    '{task:$id, arm:$arm, run:$idx, findings_fail:$fail, findings_warn:$warn,
      context_calls:$calls, committed:$committed, verified:$verified,
      reproposed:$reproposed} + $agent' >> "$RESULTS"

  if [ "$KEEP" = 0 ]; then
    git -C "$REPO" worktree remove --force "$wt"
    git -C "$REPO" branch -D "$branch" >/dev/null
  fi
}

jq -c '.[]' "$TASKS" | while read -r task; do
  for arm in without with; do
    for i in $(seq 1 "$RUNS"); do run_one "$task" "$arm" "$i"; done
  done
done

echo "done → $RESULTS (summarize with ./summarize.sh $RESULTS)" >&2
