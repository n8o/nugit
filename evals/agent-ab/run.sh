#!/usr/bin/env bash
# agent-ab runner: same tasks, two arms (with/without the nugit MCP tool),
# scored deterministically by `nugit pr-render -format json`. See README.md.
set -euo pipefail

TASKS="" REPO="" RUNS=1 OUT="results" JUDGE=0 KEEP=0 MODEL="" GUARD=""
while getopts "t:r:n:o:m:g:jk" f; do
  case $f in
    t) TASKS=$OPTARG ;;
    r) REPO=$OPTARG ;;
    n) RUNS=$OPTARG ;;
    o) OUT=$OPTARG ;;
    m) MODEL=$OPTARG ;;
    g) GUARD=$OPTARG ;;
    j) JUDGE=1 ;;
    k) KEEP=1 ;;
    *) exit 2 ;;
  esac
done
[ -n "$TASKS" ] && [ -n "$REPO" ] || { echo "usage: $0 -t tasks.json -r repo [-n runs] [-o outdir] [-m model] [-g guard-cmd] [-j] [-k]" >&2; exit 2; }
for bin in claude nugit jq; do command -v "$bin" >/dev/null || { echo "missing: $bin" >&2; exit 2; }; done

mkdir -p "$OUT"
RESULTS="$OUT/results.jsonl"
NUDGE="Before editing any file, call the nugit context tool (server nugit) with that file's path and your task. In-scope decisions list rejected alternatives; do not re-propose them."
MCPCFG="$OUT/nugit-mcp.json"
printf '{"mcpServers":{"nugit":{"command":"nugit","args":["mcp"]}}}\n' > "$MCPCFG"
# "without" arm: an empty MCP config (used with --strict-mcp-config, so the
# target's own .mcp.json is ignored) and a stub `nugit` that fails and logs.
EMPTYCFG="$OUT/empty-mcp.json"
printf '{"mcpServers":{}}\n' > "$EMPTYCFG"
STUBDIR="$(cd "$OUT" && pwd)/stub-bin"
mkdir -p "$STUBDIR"
cat > "$STUBDIR/nugit" <<'STUB'
#!/bin/sh
echo "$*" >> "${NUGIT_STUB_LOG:-/dev/null}"
echo "nugit: command not available" >&2
exit 127
STUB
chmod +x "$STUBDIR/nugit"
HERE="$(cd "$(dirname "$0")" && pwd)"

run_one() { # task_json arm run_idx
  local task=$1 arm=$2 idx=$3
  local id prompt path base verify
  id=$(jq -r .id <<<"$task"); prompt=$(jq -r .prompt <<<"$task")
  path=$(jq -r .path <<<"$task"); base=$(jq -r '.base // "origin/master"' <<<"$task")
  verify=$(jq -r '.verify // ""' <<<"$task")

  if [ -n "$GUARD" ] && ! bash -c "$GUARD"; then echo "guard stopped the run before $id/$arm/$idx" >&2; exit 3; fi

  local wt="$OUT/wt-$id-$arm-$idx" branch="eval/$id-$arm-$idx"
  rm -rf "$wt"; git -C "$REPO" worktree remove --force "$wt" 2>/dev/null || true
  git -C "$REPO" branch -D "$branch" 2>/dev/null || true
  git -C "$REPO" worktree add "$wt" -b "$branch" "$base" >/dev/null

  local -a flags=(-p --output-format json --permission-mode acceptEdits)
  [ -n "$MODEL" ] && flags+=(--model "$MODEL")
  local stublog scrubbed=""
  stublog="$(cd "$OUT" && pwd)/$id-$arm-$idx.nugit-stub.log"
  : > "$stublog"
  local -a agent_env=(env "NUGIT_STUB_LOG=$stublog")
  if [ "$arm" = with ]; then
    flags+=(--mcp-config "$(cd "$(dirname "$MCPCFG")" && pwd)/$(basename "$MCPCFG")" --append-system-prompt "$NUDGE")
  else
    "$HERE/scrub-nugit.sh" "$wt"
    scrubbed=$(git -C "$wt" ls-files -d -m)
    flags+=(--strict-mcp-config --mcp-config "$(cd "$(dirname "$EMPTYCFG")" && pwd)/$(basename "$EMPTYCFG")")
    agent_env+=("PATH=$STUBDIR:$PATH")
  fi

  echo ">> $id / $arm / run $idx" >&2
  local agent_json
  agent_json=$( (cd "$wt" && "${agent_env[@]}" claude "${flags[@]}" -- "$prompt" </dev/null) 2>"$OUT/$id-$arm-$idx.stderr" || echo '{}')
  printf '%s' "$agent_json" > "$OUT/$id-$arm-$idx.agent.json"
  jq -e '.type == "result" and (.is_error | not)' <<<"$agent_json" >/dev/null 2>&1 \
    || echo "!! agent failed: $id/$arm/$idx (see $OUT/$id-$arm-$idx.stderr); exclude this run" >&2

  # Commit whatever the agent left uncommitted so pr-render (which reads the
  # reviewed ref, not the working tree) can score it.
  # Undo the "without" arm's scrub first so it is not part of the scored diff.
  if [ -n "$scrubbed" ]; then
    # shellcheck disable=SC2086
    (cd "$wt" && git checkout -- $scrubbed)
  fi
  git -C "$wt" add -A >/dev/null
  local committed=true
  git -C "$wt" diff --cached --quiet && git -C "$wt" diff "$base"..HEAD --quiet 2>/dev/null && committed=false
  git -C "$wt" commit -q -m "eval: $id ($arm run $idx)" --allow-empty >/dev/null

  local render fail warn
  render=$(nugit pr-render -C "$wt" -base "$base" -head HEAD -format json -fail-on none 2>/dev/null || echo '{}')
  printf '%s' "$render" > "$OUT/$id-$arm-$idx.render.json"
  fail=$(jq '[.Findings[]? | select(.Severity=="fail")] | length' <<<"$render")
  warn=$(jq '[.Findings[]? | select(.Severity=="warn")] | length' <<<"$render")

  # Manipulation / contamination check. context_calls: the worktree's usage
  # log (restored .nugit is empty of cache in the "without" arm). stub_calls:
  # times the agent shelled out to the nugit stub. mcp_calls: nugit MCP
  # tool_use entries in the session transcript. The "without" arm must be 0/0/0.
  local calls=0 stub_calls mcp_calls=0 tdir
  [ -f "$wt/.nugit/.cache/usage.jsonl" ] && calls=$(wc -l < "$wt/.nugit/.cache/usage.jsonl" | tr -d ' ')
  stub_calls=$(wc -l < "$stublog" | tr -d ' ')
  # claude names the transcript dir after the worktree path, ending in its basename.
  tdir=$(find "$HOME/.claude/projects" -maxdepth 1 -type d -name "*-$(basename "$wt")" 2>/dev/null | head -1)
  if [ -n "$tdir" ]; then
    mcp_calls=$(cat "$tdir"/*.jsonl 2>/dev/null | { grep -o '"name":"mcp__nugit__[A-Za-z_]*"' || true; } | wc -l | tr -d ' ')
  fi
  local contaminated=false
  [ "$arm" = without ] && [ $((calls + stub_calls + mcp_calls)) -gt 0 ] && contaminated=true
  [ "$contaminated" = true ] && echo "!! CONTAMINATED: $id/$arm/$idx (context=$calls stub=$stub_calls mcp=$mcp_calls)" >&2

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
    --argjson stub "$stub_calls" --argjson mcp "$mcp_calls" --argjson contaminated "$contaminated" \
    --argjson committed "$committed" --argjson verified "$verified" --argjson reproposed "$reproposed" \
    --argjson agent "$(jq '{cost_usd: (.total_cost_usd // null), num_turns: (.num_turns // null), duration_ms: (.duration_ms // null)}' <<<"$agent_json")" \
    '{task:$id, arm:$arm, run:$idx, findings_fail:$fail, findings_warn:$warn,
      context_calls:$calls, stub_calls:$stub, mcp_calls:$mcp, contaminated:$contaminated, committed:$committed, verified:$verified,
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
