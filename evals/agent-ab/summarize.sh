#!/usr/bin/env bash
# summarize.sh [results.jsonl]
# Per-arm table, then PAIRED comparisons of each arm against `none` (overall
# and stratified by task tags and model), then the manipulation check.
#
# Pairing unit: a (task, model) pair. Each arm's per-pair score is the mean
# over its runs of hidden-test pass (1/0) and of file overlap. A pair counts
# for an arm-vs-none comparison only when both arms have a scored hidden-test
# result there. Runs whose task's reference check FAILED (the hidden tests do
# not pass on the real solution) are excluded from every pass rate;
# reference_pass null (no solution to check, e.g. an old-format task with only
# `verify`) is kept. p is an exact two-sided sign test over wins vs losses
# (ties dropped).
set -euo pipefail
RESULTS=${1:-"$(cd "$(dirname "$0")" && pwd)/results/results.jsonl"}
[ -f "$RESULTS" ] || { echo "no results at $RESULTS" >&2; exit 2; }

jq -s -r '
  def norm: .arm |= (if . == "with" then "nugit" elif . == "without" then "none" else . end);
  def mean(f): (map(f) | map(select(. != null)) | if length == 0 then null else add / length end);
  def fmt: if . == null then "-" elif type == "number" then ((. * 100 | round) / 100 | tostring) else tostring end;
  def pct: if . == null then "-" else ((. * 1000 | round) / 10 | tostring) + "%" end;
  def hp: if .reference_pass == false then null
          elif .hidden_tests_result == "pass" then 1
          elif .hidden_tests_result == "fail" then 0 else null end;
  def choose(n; k): reduce range(0; k) as $i (1; . * (n - $i) / ($i + 1));
  def signp(w; l): (w + l) as $n
    | if $n == 0 then null
      else ([w, l] | min) as $k
      | (([range(0; $k + 1) | choose($n; .)] | add) / pow(2; $n) * 2) | if . > 1 then 1 else . end end;
  def bucket: if . == null then "unknown" elif . == 0 then "0" elif . <= 5 then "1-5" else "6+" end;
  def armorder: {"none":0, "nugit":1, "oracle":2, "dump":3}[.] // 9;
  def paired($ps; $a):
    ($ps | map(select(.arms[$a].hp != null and .arms["none"].hp != null))) as $p
    | ($p | map(select(.arms[$a].hp > .arms["none"].hp)) | length) as $w
    | ($p | map(select(.arms[$a].hp < .arms["none"].hp)) | length) as $l
    | ($ps | map(select(.arms[$a].ov != null and .arms["none"].ov != null)) | map(.arms[$a].ov - .arms["none"].ov)) as $d
    | { n: ($p | length), w: $w, l: $l, t: (($p | length) - $w - $l), p: signp($w; $l),
        dov: (if ($d | length) == 0 then null else ($d | add / length) end), nov: ($d | length) };
  def row($label; $ps; $a): paired($ps; $a) as $s
    | [ $label, $a, $s.n, $s.w, $s.l, $s.t, ($s.p | fmt), ($s.dov | fmt), $s.nov,
        (if $s.n < 10 then "hint, not result (<10 paired)" else "" end) ] | @tsv;
  def phead: ["stratum","arm","paired","wins","losses","ties","p(sign)","delta-overlap(mean)","n(overlap)","note"] | @tsv;
  def stratum($dim): if $dim == "decision_density" then (.tags.decision_density | bucket)
                     elif $dim == "model" then .model
                     else (.tags[$dim] // "unknown" | tostring) end;

  map(norm) as $all
  | ($all | map(select(.skipped == null and .leak_check != "fail"))) as $runs
  | ($runs | map(.arm) | unique | sort_by(armorder)) as $arms
  | ($arms | map(select(. != "none"))) as $treat
  | ($runs | group_by(.task + "|" + .model) | map(
      { model: .[0].model, tags: (.[0].tags // {}),
        arms: (group_by(.arm) | map({key: .[0].arm, value: {hp: mean(hp), ov: mean(.file_overlap)}}) | from_entries) })) as $pairs
  | ($runs | map(select(.arm == "nugit"))) as $nug

  | "== per arm (runs) ==",
    (["arm","runs","committed","hidden-scored","hidden-pass","overlap(mean)","reproposed(heur)","fail(avg)","warn(avg)","turns(avg)","cost(avg$)","ctx-calls(avg)","over-limit","contaminated","skipped"] | @tsv),
    ($arms[] as $a
      | ($runs | map(select(.arm == $a))) as $r
      | [ $a, ($r | length), ($r | map(select(.committed == true)) | length),
          ($r | map(hp) | map(select(. != null)) | length),
          ($r | mean(hp) | pct), ($r | mean(.file_overlap) | fmt),
          "\($r | map(select(.reproposed_heuristic == true)) | length)/\($r | map(select(.reproposed_heuristic != null)) | length)",
          ($r | mean(.findings_fail) | fmt), ($r | mean(.findings_warn) | fmt),
          ($r | mean(.num_turns) | fmt), ($r | mean(.cost_usd) | fmt), ($r | mean(.context_calls) | fmt),
          ($r | map(select(.context_over_limit == true)) | length),
          ($r | map(select(.contaminated == true)) | length),
          ($all | map(select(.arm == $a and .skipped != null)) | length) ] | @tsv),
    "",
    "== paired vs none (unit: task x model; hidden-test pass; exact sign test) ==",
    phead,
    ($treat[] as $a | row("all"; $pairs; $a)),
    "",
    ( ("decision_density", "kind", "has_rejected", "model") as $dim
      | "== stratified by \($dim) ==",
        phead,
        ( $pairs | map(. + {s: stratum($dim)}) | group_by(.s)[] as $g
          | $treat[] as $a | row("\($dim)=\($g[0].s)"; $g; $a) ),
        "" ),
    "== manipulation / validity checks ==",
    "nugit arm: \($nug | length) runs; \($nug | map(select((.context_calls // 0) == 0)) | length) with 0 context() calls; \($nug | map(select(.context_over_limit == true)) | length) with an over-limit bundle (the agent got a file path, not the bundle)",
    "control arms (none/oracle/dump): \($runs | map(select(.arm != "nugit" and .contaminated == true)) | length) contaminated runs (must be 0)",
    "agent failures (agent_ok false): \($runs | map(select(.agent_ok == false)) | length)",
    "tasks with reference_pass=false (excluded from pass rates): \($all | map(select(.reference_pass == false) | .task) | unique | length)",
    "leak_check fail lines: \($all | map(select(.leak_check == "fail")) | length)",
    "oracle skipped (no-oracle): \($all | map(select(.skipped == "no-oracle")) | length)",
    "dump truncated at the cap: \($runs | map(select(.dump_truncated == true)) | length)",
    "reproposed(heur) is a term-match HEURISTIC (repropose.sh): read transcripts before quoting it."
' "$RESULTS" | awk -F'\t' '
  # Align each run of tab-separated lines as a table; pass other lines through.
  function flush(   i, j, line) {
    for (i = 1; i <= n; i++) {
      line = ""
      for (j = 1; j <= nf[i]; j++) line = line sprintf("%-" (w[j] + 2) "s", cell[i, j])
      sub(/ +$/, "", line); print line
    }
    n = 0; split("", w)
  }
  /\t/ { n++; nf[n] = NF; for (j = 1; j <= NF; j++) { cell[n, j] = $j; if (length($j) > w[j]) w[j] = length($j) } next }
  { if (n) flush(); print }
  END { if (n) flush() }'
