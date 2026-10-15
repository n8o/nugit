#!/usr/bin/env bash
# agent-ab replay runner: replay historical tasks (tasks.json, from
# `nugit export -format tasks`) through a headless agent in up to four arms,
# each in a leak-free clone at the task's base, and score every run with the
# task's hidden tests, file overlap with the real solution, a re-proposal
# heuristic, and `nugit pr-render -format json`. See README.md.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
usage() {
  cat >&2 <<'EOF'
usage: run.sh -t tasks.json -r repo [options]
  -t file     tasks.json (an array; `nugit export -format tasks` or the old example format)
  -r dir      the source repo the tasks were exported from
  -a arms     comma list of none,nugit,oracle,dump (default: all four;
              aliases: without=none, with=nugit)
  -n N        runs per arm per task (default 1)
  -m model    model passed to claude --model (default: claude's default)
  -b path     nugit binary for the nugit arm AND for scoring (default: nugit on PATH)
  -l N        only the first N tasks (after -f)
  -f expr     jq filter on each task, e.g. '.tags.has_hidden_tests'
  -o dir      results dir (default: evals/agent-ab/results)
  -w dir      where per-run clones go (default: a fresh mktemp -d, outside any repo)
  -k          keep clones for inspection (default: delete after scoring)
  -g cmd      extra guard command run before every run; non-zero stops the batch
  -Q5 pct     stop cleanly when agent-deck's five_hour used_percentage >= pct (default 80)
  -Q7 pct     stop cleanly when agent-deck's seven_day used_percentage >= pct (default 50)
  -j          also run the opt-in LLM re-proposal judge (spends quota)
  -T          tests visible: commit the task's hidden tests into the clone before
              the run and tell the agent to make them pass (TDD mode). Use when
              the tests call internal names the task text cannot reveal. They are
              re-applied from the solution before scoring, so editing them gains nothing.
EOF
  exit 2
}

TASKS="" REPO="" RUNS=1 OUT="$HERE/results" JUDGE=0 KEEP=0 MODEL="" GUARD=""
ARMS="none,nugit,oracle,dump" TESTS_VISIBLE=0 NUGIT_BIN="" LIMIT="" FILTER="" WORK="" Q5=80 Q7=50
while [ $# -gt 0 ]; do
  case $1 in
    -t) TASKS=$2; shift ;;
    -r) REPO=$2; shift ;;
    -a) ARMS=$2; shift ;;
    -n) RUNS=$2; shift ;;
    -m) MODEL=$2; shift ;;
    -b) NUGIT_BIN=$2; shift ;;
    -l) LIMIT=$2; shift ;;
    -f) FILTER=$2; shift ;;
    -o) OUT=$2; shift ;;
    -w) WORK=$2; shift ;;
    -g) GUARD=$2; shift ;;
    -Q5) Q5=$2; shift ;;
    -Q7) Q7=$2; shift ;;
    -k) KEEP=1 ;;
    -j) JUDGE=1 ;;
    -T) TESTS_VISIBLE=1 ;;
    -h|--help) usage ;;
    *) echo "unknown flag: $1" >&2; usage ;;
  esac
  shift
done
[ -n "$TASKS" ] && [ -n "$REPO" ] || usage
for bin in claude git jq; do command -v "$bin" >/dev/null || { echo "missing: $bin" >&2; exit 2; }; done
if [ -z "$NUGIT_BIN" ]; then NUGIT_BIN=$(command -v nugit || true); fi
[ -n "$NUGIT_BIN" ] && [ -x "$NUGIT_BIN" ] || { echo "missing: nugit (or -b path)" >&2; exit 2; }
NUGIT_BIN="$(cd "$(dirname "$NUGIT_BIN")" && pwd)/$(basename "$NUGIT_BIN")"
REPO="$(cd "$REPO" && pwd)"

# Normalize the arm list (old names are aliases).
arm_list=""
for a in ${ARMS//,/ }; do
  case $a in
    none|without) a=none ;;
    nugit|with) a=nugit ;;
    oracle|dump) ;;
    *) echo "unknown arm: $a (want none,nugit,oracle,dump)" >&2; exit 2 ;;
  esac
  case " $arm_list " in *" $a "*) ;; *) arm_list+=" $a" ;; esac
done

mkdir -p "$OUT/runs" "$OUT/reference"
OUT="$(cd "$OUT" && pwd)"
RESULTS="$OUT/results.jsonl"
[ -n "$WORK" ] || WORK=$(mktemp -d "${TMPDIR:-/tmp}/agent-ab.XXXXXX")
mkdir -p "$WORK"; WORK="$(cd "$WORK" && pwd)"

NUDGE="Before editing any file, call the nugit context tool (server nugit) with that file's path and your task. In-scope decisions list rejected alternatives; do not re-propose them."

# MCP configs. Every arm runs with --strict-mcp-config, so the target repo's own
# .mcp.json (and any user-level server) is ignored identically; only the nugit
# arm gets a server, pointing at the chosen binary.
MCPCFG="$OUT/nugit-mcp.json"
jq -nc --arg cmd "$NUGIT_BIN" '{mcpServers:{nugit:{command:$cmd, args:["mcp"]}}}' > "$MCPCFG"
EMPTYCFG="$OUT/empty-mcp.json"
printf '{"mcpServers":{}}\n' > "$EMPTYCFG"

# PATH shims. No-nugit arms: a stub that logs and exits 127. nugit arm: `nugit`
# resolves to the chosen binary (for CLAUDE.md-wired CLI calls and hooks).
STUBDIR="$OUT/stub-bin"; mkdir -p "$STUBDIR"
cat > "$STUBDIR/nugit" <<'STUB'
#!/bin/sh
echo "$*" >> "${NUGIT_STUB_LOG:-/dev/null}"
echo "nugit: command not available" >&2
exit 127
STUB
chmod +x "$STUBDIR/nugit"
SHIMDIR="$OUT/nugit-bin"; mkdir -p "$SHIMDIR"
ln -sf "$NUGIT_BIN" "$SHIMDIR/nugit"

# Network-ish and escape-hatch tools are denied identically in every arm. The
# Read/Grep/Glob rules on the source repo are best-effort (the object-level
# guarantee is the clone); git -C would let Bash reach the source repo.
DENY=(WebFetch WebSearch "Bash(gh:*)" "Bash(git fetch:*)" "Bash(git pull:*)" "Bash(curl:*)"
      "Bash(git -C:*)" "Read(/$REPO/**)" "Grep(/$REPO/**)" "Glob(/$REPO/**)")

# Local, non-network commands every arm may run without a prompt (headless -p
# has no one to approve them; a repo with no committed permissions would leave
# the agent unable to build, test or commit). Same list for every arm; the DENY
# list above still wins. Override with AB_ALLOW (space-separated rules).
read -r -a ALLOW <<<"${AB_ALLOW:-Bash(go:*) Bash(gofmt:*) Bash(make:*) Bash(git status:*) Bash(git diff:*) Bash(git log:*) Bash(git show:*) Bash(git add:*) Bash(git commit:*) Bash(git grep:*) Bash(ls:*) Bash(cat:*) Bash(head:*) Bash(tail:*) Bash(wc:*) Bash(grep:*) Bash(rg:*) Bash(find:*) Bash(sed -n:*) Bash(jq:*) Bash(python3 -m pytest:*) Bash(npx vitest:*) Bash(npx jest:*)}"

# --- quota guard -----------------------------------------------------------
quota_guard() {
  command -v agent-deck >/dev/null 2>&1 || return 0
  local u f s
  u=$(agent-deck usage --json 2>/dev/null) || return 0
  f=$(jq -r '[.providers[]? | select(.id=="claude") | .windows[]? | select(.kind=="five_hour") | .used_percentage] | max // empty' <<<"$u" 2>/dev/null || true)
  s=$(jq -r '[.providers[]? | select(.id=="claude") | .windows[]? | select(.kind=="seven_day") | .used_percentage] | max // empty' <<<"$u" 2>/dev/null || true)
  if [ -n "$f" ] && awk -v a="$f" -v b="$Q5" 'BEGIN{exit !(a>=b)}'; then
    echo "quota guard: five_hour used ${f}% >= ${Q5}% — stopping cleanly; results so far are in $RESULTS" >&2; exit 0
  fi
  if [ -n "$s" ] && awk -v a="$s" -v b="$Q7" 'BEGIN{exit !(a>=b)}'; then
    echo "quota guard: seven_day used ${s}% >= ${Q7}% — stopping cleanly; results so far are in $RESULTS" >&2; exit 0
  fi
}

# --- helpers -----------------------------------------------------------------
is_test_file() { # path → 0 when it looks like a test
  case $1 in
    *_test.go|*.test.*|*.spec.*|test_*.py|*_test.py|*/test_*.py|tests/*|test/*|*/tests/*|*/test/*|*/__tests__/*|__tests__/*) return 0 ;;
  esac
  return 1
}
scored_files() { # newline list on stdin → non-test, non-store files, sorted unique
  local f
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    case $f in .nugit/*|.beads/*) continue ;; esac
    is_test_file "$f" && continue
    printf '%s\n' "$f"
  done | sort -u
}
jaccard() { # fileA fileB (sorted lists) → number or null
  local inter uni
  inter=$(comm -12 "$1" "$2" | wc -l | tr -d ' ')
  uni=$(sort -u "$1" "$2" | wc -l | tr -d ' ')
  if [ "$uni" -eq 0 ]; then echo null; else awk -v i="$inter" -v u="$uni" 'BEGIN{printf "%.4g", i/u}'; fi
}
apply_hidden() { # task_json dir solution → copies the solution's hidden test blobs into dir
  local task=$1 dir=$2 sol=$3 f
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    if git -C "$REPO" cat-file -e "$sol:$f" 2>/dev/null; then
      mkdir -p "$dir/$(dirname "$f")"
      git -C "$REPO" show "$sol:$f" > "$dir/$f"
    fi
  done < <(jq -r '.hidden_tests // [] | .[]' <<<"$task")
}

# Reference check: the hidden tests against the real solution, once per task
# (cached in $OUT/reference/<id>.json, keyed by solution + test list + verify).
reference_check() { # task_json → prints {reference_pass, reference_exit}
  local task=$1 id sol verify key cache dir ht
  id=$(jq -r .id <<<"$task"); sol=$(jq -r '.source.solution // .solution // ""' <<<"$task")
  verify=$(jq -r '.verify // ""' <<<"$task")
  if [ -z "$sol" ] || { [ "$(jq '.hidden_tests // [] | length' <<<"$task")" -eq 0 ] && [ -z "$verify" ]; }; then
    jq -nc '{reference_pass:null, reference_exit:null}'; return
  fi
  key=$(jq -c '[(.source.solution // .solution), (.hidden_tests // []), (.verify // "")]' <<<"$task")
  cache="$OUT/reference/$id.json"
  if [ -f "$cache" ] && [ "$(jq -c .key "$cache")" = "$key" ]; then jq -c '{reference_pass, reference_exit}' "$cache"; return; fi
  dir="$WORK/ref-$id-$RANDOM"
  "$HERE/clone-at.sh" "$REPO" "$sol" "$dir" "eval/reference" >/dev/null
  # shellcheck disable=SC2046
  ht=$(HT_LOG="$OUT/reference/$id.log" "$HERE/hidden-tests.sh" "$dir" "$verify" $(jq -r '.hidden_tests // [] | .[]' <<<"$task"))
  rm -rf "$dir"
  jq -nc --argjson key "$key" --argjson ht "$ht" \
    '{key:$key, reference_pass:(if $ht.result=="pass" then true elif $ht.result=="fail" then false else null end),
      reference_exit:$ht.exit}' > "$cache"
  jq -c '{reference_pass, reference_exit}' "$cache"
}

run_one() { # task_json arm run_idx reference_json
  local task=$1 arm=$2 idx=$3 ref=$4
  local id prompt path base_ref verify sol base
  id=$(jq -r .id <<<"$task"); prompt=$(jq -r .prompt <<<"$task")
  path=$(jq -r '.path // ""' <<<"$task"); base_ref=$(jq -r '.base // "origin/main"' <<<"$task")
  verify=$(jq -r '.verify // ""' <<<"$task")
  sol=$(jq -r '.source.solution // .solution // ""' <<<"$task")
  base=$(git -C "$REPO" rev-parse --verify --quiet "$base_ref^{commit}") || { echo "!! $id: base $base_ref not found in $REPO; skipping" >&2; return 0; }
  local rid="$id-$arm-$idx" tags
  tags=$(jq -c '.tags // {}' <<<"$task")
  local rdir="$OUT/runs/$rid"; rm -rf "$rdir"; mkdir -p "$rdir"

  local base_line
  base_line=$(jq -nc --arg id "$id" --arg arm "$arm" --argjson idx "$idx" --arg model "${MODEL:-default}" \
    --arg base "$base" --arg sol "$sol" --argjson tags "$tags" --argjson ref "$ref" \
    --argjson tv "$TESTS_VISIBLE" \
    '{task:$id, arm:$arm, run:$idx, model:$model, base:$base, solution:$sol, tags:$tags, tests_visible:($tv==1)} + $ref')

  # Arm-specific system-prompt text (built from the store AT BASE).
  local sysfile="" kmeta='{}'
  case $arm in
    nugit) sysfile="$rdir/system-prompt.txt"; printf '%s\n' "$NUDGE" > "$sysfile" ;;
    oracle)
      local keys
      keys=$(jq -r '.oracle_keys // [] | .[]' <<<"$task")
      if [ -z "$keys" ]; then
        jq -c '. + {skipped:"no-oracle"}' <<<"$base_line" >> "$RESULTS"; echo ">> $rid: skipped (no oracle_keys)" >&2; return 0
      fi
      sysfile="$rdir/system-prompt.txt"
      # shellcheck disable=SC2086
      kmeta=$("$HERE/knowledge-text.sh" oracle "$REPO" "$base" "$sysfile" $keys)
      if [ "$(jq .chars <<<"$kmeta")" -eq 0 ]; then
        jq -c --argjson k "$kmeta" '. + {skipped:"no-oracle", oracle_missing:$k.missing}' <<<"$base_line" >> "$RESULTS"
        echo ">> $rid: skipped (no oracle key found at base: $(jq -c .missing <<<"$kmeta"))" >&2; return 0
      fi
      kmeta=$(jq -c '{oracle_chars:.chars, oracle_files:.files, oracle_missing:.missing}' <<<"$kmeta") ;;
    dump)
      sysfile="$rdir/system-prompt.txt"
      kmeta=$("$HERE/knowledge-text.sh" dump "$REPO" "$base" "$sysfile" "${DUMP_CAP:-120000}")
      kmeta=$(jq -c '{dump_chars:.chars, dump_files:.files, dump_truncated:.truncated}' <<<"$kmeta") ;;
  esac

  [ -n "$GUARD" ] && ! bash -c "$GUARD" && { echo "guard stopped the run before $rid" >&2; exit 0; }
  quota_guard

  # Leak-free clone at base; the solution must be unreachable.
  local wt="$WORK/ab-$rid-$RANDOM" branch="eval/$rid" leak
  leak=$("$HERE/clone-at.sh" "$REPO" "$base" "$wt" "$branch" "$sol" | sed -n 's/^leak_check=//p')
  if [ "$leak" = fail ]; then
    jq -c '. + {leak_check:"fail"}' <<<"$base_line" >> "$RESULTS"
    echo "!! LEAK CHECK FAILED for $rid: solution $sol is reachable in $wt — aborting the batch" >&2
    exit 1
  fi

  # Score against sbase: base, or base + the committed acceptance tests (-T),
  # so the visible tests never count as the agent's work.
  local sbase=$base
  local -a htests=()
  while IFS= read -r f; do [ -n "$f" ] && htests+=("$f"); done < <(jq -r '.hidden_tests // [] | .[]' <<<"$task")
  if [ "$TESTS_VISIBLE" = 1 ] && [ ${#htests[@]} -gt 0 ] && [ -n "$sol" ]; then
    for f in "${htests[@]}"; do
      mkdir -p "$wt/$(dirname "$f")"
      git -C "$REPO" show "$sol:$f" > "$wt/$f" 2>/dev/null || rm -f "$wt/$f"
    done
    git -C "$wt" add -- "${htests[@]}" >/dev/null 2>&1 || true
    git -C "$wt" commit -q -m "eval: acceptance tests for $id" >/dev/null 2>&1 || true
    sbase=$(git -C "$wt" rev-parse HEAD)
    prompt=$(printf '%s\n\nAcceptance tests for this task are already committed: %s. Make them pass without editing them.' "$prompt" "${htests[*]}")
  fi

  local -a flags=(-p --output-format json --permission-mode acceptEdits --strict-mcp-config)
  [ -n "$MODEL" ] && flags+=(--model "$MODEL")
  local stublog="$rdir/nugit-stub.log" scrubbed=""
  : > "$stublog"
  local -a agent_env=(env "NUGIT_STUB_LOG=$stublog")
  if [ "$arm" = nugit ]; then
    flags+=(--mcp-config "$MCPCFG")
    agent_env+=("PATH=$SHIMDIR:$PATH")
  else
    "$HERE/scrub-nugit.sh" "$wt"
    scrubbed=$(git -C "$wt" ls-files -d -m)
    flags+=(--mcp-config "$EMPTYCFG")
    agent_env+=("PATH=$STUBDIR:$PATH")
  fi
  [ -n "$sysfile" ] && flags+=(--append-system-prompt "$(cat "$sysfile")")
  flags+=(--disallowedTools "${DENY[@]}")
  flags+=(--allowedTools "${ALLOW[@]}")

  echo ">> $id / $arm / run $idx" >&2
  local t_start t_end agent_json
  t_start=$(date +%s)
  agent_json=$( (cd "$wt" && "${agent_env[@]}" claude "${flags[@]}" -- "$prompt" </dev/null) 2>"$rdir/stderr" || true)
  t_end=$(date +%s)
  jq -e . >/dev/null 2>&1 <<<"$agent_json" || agent_json='{}'
  printf '%s' "$agent_json" > "$rdir/agent.json"
  jq -e '.type == "result" and (.is_error | not)' <<<"$agent_json" >/dev/null 2>&1 \
    || echo "!! agent failed: $rid (see $rdir/stderr); exclude this run" >&2

  # Undo the scrub, then commit whatever the agent left uncommitted so
  # pr-render (which reads refs, not the working tree) can score it. Restore
  # from BASE, not the index: an agent that ran `git add -A` may already have
  # committed the scrub's deletions; restoring from base nets them out of the
  # scored base..HEAD diff either way.
  if [ -n "$scrubbed" ]; then
    # shellcheck disable=SC2086
    (cd "$wt" && git checkout "$base" -- $scrubbed)
  fi
  git -C "$wt" add -A -- . ':(exclude).nugit/.cache' >/dev/null
  local committed=true
  git -C "$wt" diff --cached --quiet && git -C "$wt" diff "$sbase"..HEAD --quiet 2>/dev/null && committed=false
  git -C "$wt" commit -q -m "eval: $id ($arm run $idx)" --allow-empty >/dev/null
  git -C "$wt" diff "$sbase"..HEAD > "$rdir/diff.patch" || true

  local render fail warn
  render=$("$NUGIT_BIN" pr-render -C "$wt" -base "$sbase" -head HEAD -format json -fail-on none 2>/dev/null || echo '{}')
  jq -e . >/dev/null 2>&1 <<<"$render" || render='{}'
  printf '%s' "$render" > "$rdir/render.json"
  fail=$(jq '[.Findings[]? | select(.Severity=="fail")] | length' <<<"$render")
  warn=$(jq '[.Findings[]? | select(.Severity=="warn")] | length' <<<"$render")

  # Manipulation / contamination check. The clone is its own repo, so nugit's
  # usage log is the clone's own .nugit/.cache/usage.jsonl; every record in the
  # run's window is this run's (branch "*").
  local calls stub_calls mcp_calls=0 tdir ctx_json
  ctx_json=$("$HERE/usage-window.sh" "$wt" "*" "$t_start" "$((t_end + 5))")
  calls=$(jq .calls <<<"$ctx_json")
  stub_calls=$(wc -l < "$stublog" | tr -d ' ')
  # claude names the transcript dir after the cwd path, ending in its basename.
  tdir=$(find "$HOME/.claude/projects" -maxdepth 1 -type d -name "*-$(basename "$wt")" 2>/dev/null | head -1)
  if [ -n "$tdir" ]; then
    cat "$tdir"/*.jsonl > "$rdir/transcript.jsonl" 2>/dev/null || true
    mcp_calls=$({ grep -o '"name":"mcp__nugit__[A-Za-z_]*"' "$rdir/transcript.jsonl" || true; } | wc -l | tr -d ' ')
  fi
  local contaminated=false
  [ "$arm" != nugit ] && [ $((calls + stub_calls + mcp_calls)) -gt 0 ] && contaminated=true
  [ "$contaminated" = true ] && echo "!! CONTAMINATED: $rid (context=$calls stub=$stub_calls mcp=$mcp_calls)" >&2

  # File overlap with the real solution (non-test, no .nugit/.beads).
  local overlap=null touched
  git -C "$wt" diff --name-only "$sbase"..HEAD | scored_files > "$rdir/touched.txt"
  jq -r '.solution_files // [] | .[]' <<<"$task" | scored_files > "$rdir/solution-files.txt"
  touched=$(wc -l < "$rdir/touched.txt" | tr -d ' ')
  if [ -s "$rdir/solution-files.txt" ]; then overlap=$(jaccard "$rdir/touched.txt" "$rdir/solution-files.txt"); fi

  # Re-proposal heuristic (deterministic, labelled heuristic).
  jq -c '.rejected // []' <<<"$task" > "$rdir/rejected.json"
  local rep
  rep=$("$HERE/repropose.sh" "$wt" "$sbase" "$rdir/rejected.json")

  # Optional LLM judge (old -j behaviour; prefers the task's rejected texts).
  local reproposed=null
  if [ "$JUDGE" = 1 ] && [ "$committed" = true ]; then
    local rejected diff judge_prompt
    rejected=$(jq -r 'map(.text) | join("\n---\n")' "$rdir/rejected.json")
    [ -n "$rejected" ] || rejected=$("$NUGIT_BIN" context -C "$wt" -path "$path" -format json 2>/dev/null | jq -r '[.decisions[]?.rejected // empty] | join("\n---\n")' || true)
    diff=$(head -c 30000 "$rdir/diff.patch")
    if [ -n "$rejected" ]; then
      judge_prompt=$(printf 'You are grading a code change. REJECTED ALTERNATIVES from the project'\''s ADRs:\n%s\n\nDIFF:\n%s\n\nDoes the diff implement any rejected alternative? Answer with exactly one word: yes or no.' "$rejected" "$diff")
      if claude -p --output-format json "$judge_prompt" </dev/null 2>/dev/null | jq -r '.result // ""' | grep -qi '^yes'; then
        reproposed=true
      else
        reproposed=false
      fi
    fi
  fi

  # Hidden tests: the agent is done, so the solution's test blobs can now be
  # copied in from the source repo and run.
  local ht='{"result":"none","exit":null,"command":""}'
  if [ -n "$sol" ]; then apply_hidden "$task" "$wt" "$sol"; fi
  # shellcheck disable=SC2046
  ht=$(HT_LOG="$rdir/hidden-tests.log" "$HERE/hidden-tests.sh" "$wt" "$verify" $(jq -r '.hidden_tests // [] | .[]' <<<"$task"))

  jq -c --arg leak "${leak:-n/a}" \
    --argjson fail "$fail" --argjson warn "$warn" --argjson calls "$calls" \
    --argjson ctx "$ctx_json" --argjson stub "$stub_calls" --argjson mcp "$mcp_calls" --argjson contaminated "$contaminated" \
    --argjson committed "$committed" --argjson reproposed "$reproposed" --argjson rep "$rep" \
    --argjson ht "$ht" --argjson overlap "$overlap" --argjson touched "$touched" --argjson kmeta "$kmeta" \
    --argjson agent "$(jq '{cost_usd: (.total_cost_usd // null), num_turns: (.num_turns // null), duration_ms: (.duration_ms // null),
                            tokens: (if .usage then {input: .usage.input_tokens, output: .usage.output_tokens,
                                     cache_read: .usage.cache_read_input_tokens, cache_creation: .usage.cache_creation_input_tokens} else null end),
                            agent_ok: (.type == "result" and (.is_error | not))}' <<<"$agent_json")" \
    '. + {leak_check:$leak, committed:$committed, findings_fail:$fail, findings_warn:$warn,
      context_calls:$calls, context_hook_edits:$ctx.hook_edits, context_est_tokens:$ctx.est_tokens, context_budget_tokens:$ctx.budget_tokens,
      context_truncated:$ctx.truncated, context_over_limit:$ctx.over_limit, context_over_limit_reason:$ctx.reason,
      stub_calls:$stub, mcp_calls:$mcp, contaminated:$contaminated,
      hidden_tests_result:$ht.result, hidden_tests_exit:$ht.exit, hidden_tests_cmd:$ht.command,
      file_overlap:$overlap, files_touched:$touched,
      reproposed_heuristic:$rep.flag, reproposed_terms:$rep.terms, reproposed_matched:$rep.matched,
      reproposed:$reproposed} + $kmeta + $agent' <<<"$base_line" >> "$RESULTS"

  if [ "$KEEP" = 0 ]; then rm -rf "$wt"; else echo "   kept clone: $wt" >&2; fi
}

# --- main loop ---------------------------------------------------------------
sel='.[]'
[ -n "$FILTER" ] && sel=".[] | select($FILTER)"
task_stream=$(jq -c "[$sel]$( [ -n "$LIMIT" ] && echo " | .[:$LIMIT]" ) | .[]" "$TASKS")
[ -n "$task_stream" ] || { echo "no tasks selected" >&2; exit 0; }

while IFS= read -r -u 3 task; do
  ref=$(reference_check "$task")
  if [ "$(jq .reference_pass <<<"$ref")" = false ]; then
    echo "!! $(jq -r .id <<<"$task"): hidden tests FAIL on the real solution (reference_pass=false); runs are recorded but summarize.sh excludes them from pass rates" >&2
  fi
  for arm in $arm_list; do
    for i in $(seq 1 "$RUNS"); do run_one "$task" "$arm" "$i" "$ref"; done
  done
done 3<<<"$task_stream"

[ "$KEEP" = 1 ] || rmdir "$WORK" 2>/dev/null || true
echo "done → $RESULTS (summarize with $HERE/summarize.sh $RESULTS)" >&2
