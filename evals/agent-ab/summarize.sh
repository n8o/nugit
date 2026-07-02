#!/usr/bin/env bash
# Aggregate results.jsonl → one row per (task, arm) with means across runs.
set -euo pipefail
RESULTS=${1:-results/results.jsonl}
[ -f "$RESULTS" ] || { echo "no results at $RESULTS" >&2; exit 2; }

jq -s -r '
  def mean(f): (map(f) | map(select(. != null)) | if length==0 then null else (add/length*100|round)/100 end);
  group_by(.task + "/" + .arm)[] |
  [ .[0].task, .[0].arm, length,
    mean(.findings_fail), mean(.findings_warn), mean(.context_calls),
    (map(select(.committed)) | length),
    mean(.cost_usd), mean(.num_turns),
    (map(select(.reproposed==true)) | length) ] |
  @tsv
' "$RESULTS" | {
  printf "task\tarm\truns\tfail(avg)\twarn(avg)\tctx-calls(avg)\tcommitted\tcost(avg$)\tturns(avg)\treproposed\n"
  cat
} | column -t -s $'\t'
