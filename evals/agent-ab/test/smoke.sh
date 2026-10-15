#!/usr/bin/env bash
# End-to-end smoke test for the replay harness. Spends NO quota: a fake
# `claude` and a fake `agent-deck` (test/fake-bin/) come first on PATH.
#
# Builds a throwaway Go repo (a decision store, a base commit, a solution
# commit that adds a hidden test), runs all four arms over two tasks (one in
# the full export schema, one in the old example format), and asserts the leak
# check, hidden-test scoring, oracle/dump delivery, the nugit usage count, the
# re-proposal heuristic, summarize.sh, and the quota guard.
#
# Usage: evals/agent-ab/test/smoke.sh   (env NUGIT=/path/to/nugit to skip the build)
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
AB="$(dirname "$HERE")"
ROOT="$(cd "$AB/../.." && pwd)"
T=$(mktemp -d "${TMPDIR:-/tmp}/agent-ab-smoke.XXXXXX")
trap 'rm -rf "$T"' EXIT
fails=0
ok()   { echo "ok   - $*"; }
bad()  { echo "FAIL - $*"; fails=$((fails + 1)); }
check() { local msg=$1; shift; if "$@"; then ok "$msg"; else bad "$msg"; fi; }

if [ -z "${NUGIT:-}" ]; then
  (cd "$ROOT" && go build -o "$T/bin/nugit" ./cmd/nugit)
  NUGIT="$T/bin/nugit"
fi

# --- a tiny target repo --------------------------------------------------------
R="$T/repo"
mkdir -p "$R/calc" "$R/.nugit/decisions"
git -C "$R" init -q -b main
printf '.nugit/.cache/\n' > "$R/.gitignore"   # as real stores do: makes the cache an ignored path
git -C "$R" config user.name smoke; git -C "$R" config user.email smoke@example.invalid
printf 'module example.com/tiny\n\ngo 1.21\n' > "$R/go.mod"
printf 'package calc\n\n// Add returns a plus b.\nfunc Add(a, b int) int { return a + b }\n' > "$R/calc/calc.go"
cat > "$R/.nugit/decisions/0001-no-global-registry.md" <<'EOF'
---
schema_version: 1
id: ADR-0001
type: decision
status: accepted
---
# Arithmetic helpers are plain functions

ORACLE-MARKER-0001: keep calc stateless.

## Rejected

- A process-wide `GlobalRegistry` singleton that every helper registers in.
EOF
cat > "$R/.nugit/decisions/0002-old-approach.md" <<'EOF'
---
schema_version: 1
id: ADR-0002
type: decision
status: superseded
---
# An old approach

SUPERSEDED-MARKER-0002
EOF
cat > "$R/.nugit/decisions/0003-current.md" <<'EOF'
---
schema_version: 1
id: ADR-0003
type: decision
status: accepted
---
# Current decision

DUMP-MARKER-0003
EOF
printf '# Tiny\n\nCall the nugit context tool before editing.\n\nKeep functions small.\n' > "$R/CLAUDE.md"
git -C "$R" add -A; git -C "$R" commit -q -m "base"
BASE=$(git -C "$R" rev-parse HEAD)
printf '\n// Sub returns a minus b.\nfunc Sub(a, b int) int { return a - b }\n' >> "$R/calc/calc.go"
cat > "$R/calc/sub_test.go" <<'EOF'
package calc

import "testing"

func TestSub(t *testing.T) {
	if got := Sub(5, 3); got != 2 {
		t.Fatalf("Sub(5, 3) = %d, want 2", got)
	}
}
EOF
git -C "$R" add -A; git -C "$R" commit -q -m "feat: add Sub"
SOL=$(git -C "$R" rev-parse HEAD)

jq -n --arg base "$BASE" --arg sol "$SOL" '[
  {id:"t-sub", prompt:"Add a Sub(a, b int) int helper to package calc. Commit your work.",
   path:"calc/calc.go", base:$base, verify:"",
   source:{bead:"tiny-1", commits:[$sol], solution:$sol},
   solution_files:["calc/calc.go","calc/sub_test.go"], hidden_tests:["calc/sub_test.go"],
   oracle_keys:["ADR-0001"],
   tags:{kind:"feat", files:2, decision_density:1, churn_90d:2, has_rejected:true, has_hidden_tests:true},
   rejected:[{key:"ADR-0001", text:"A process-wide `GlobalRegistry` singleton that every helper registers in."}]},
  {id:"t-old", prompt:"Add a Sub helper to package calc. Commit your work.",
   path:"calc/calc.go", base:$base, verify:"go build ./..."}
]' > "$T/tasks.json"

export PATH="$HERE/fake-bin:$PATH"
export FAKE_CLAUDE_LOGDIR="$T/claude-logs" FAKE_SOLUTION="$SOL"
mkdir -p "$FAKE_CLAUDE_LOGDIR"
OUT="$T/results"

echo "# run (fake claude, all four arms, 2 tasks)"
"$AB/run.sh" -t "$T/tasks.json" -r "$R" -b "$NUGIT" -o "$OUT" -w "$T/work" 2> "$T/run.stderr" || { cat "$T/run.stderr"; bad "run.sh exited non-zero"; }
RES="$OUT/results.jsonl"
q() { jq -s -r "$1" "$RES"; }

check "8 result lines (2 tasks x 4 arms)"                     [ "$(q length)" = 8 ]
check "leak_check pass on every t-sub run"                    [ "$(q '[.[] | select(.task=="t-sub" and .skipped == null) | .leak_check] | unique | join(",")')" = pass ]
check "leak_check n/a for a task with no solution"           [ "$(q '[.[] | select(.task=="t-old" and .skipped == null) | .leak_check] | unique | join(",")')" = n/a ]
check "agent never saw the solution commit"                   bash -c "! grep -q '^LEAK' $FAKE_CLAUDE_LOGDIR/*.log"
check "clone has no remote"                                   bash -c "! grep -q HAS_REMOTE $FAKE_CLAUDE_LOGDIR/*.log"
check "reference_pass true for t-sub"                         [ "$(q '[.[] | select(.task=="t-sub") | .reference_pass] | unique | join(",")')" = true ]
check "none arm: hidden tests applied and FAIL"               [ "$(q '.[] | select(.task=="t-sub" and .arm=="none") | .hidden_tests_result')" = fail ]
check "oracle/dump/nugit: hidden tests PASS"                  [ "$(q '[.[] | select(.task=="t-sub" and .arm!="none") | .hidden_tests_result] | unique | join(",")')" = pass ]
check "hidden test command derived from .go"                  [ "$(q '.[] | select(.task=="t-sub" and .arm=="none") | .hidden_tests_cmd')" = "cd '.' && go test -count=1 -run '^(TestSub)$' ./calc" ]
check "old-format task scored by verify"                      [ "$(q '.[] | select(.task=="t-old" and .arm=="none") | .hidden_tests_result')" = pass ]
check "oracle text reached the agent (ADR-0001 body)"         bash -c "cat $FAKE_CLAUDE_LOGDIR/ab-t-sub-oracle-*.log | grep -q ORACLE-MARKER-0001"
check "oracle text excludes other decisions"                  bash -c "! cat $FAKE_CLAUDE_LOGDIR/ab-t-sub-oracle-*.log | grep -q DUMP-MARKER-0003"
check "dump has the effective decisions"                      bash -c "cat $FAKE_CLAUDE_LOGDIR/ab-t-sub-dump-*.log | grep -q DUMP-MARKER-0003"
check "dump excludes the superseded decision"                 bash -c "! cat $FAKE_CLAUDE_LOGDIR/ab-t-sub-dump-*.log | grep -q SUPERSEDED-MARKER-0002"
check "dump_chars recorded, not truncated"                    [ "$(q '.[] | select(.task=="t-sub" and .arm=="dump") | (.dump_chars > 0 and .dump_truncated == false)')" = true ]
check "oracle skipped for a task with no oracle_keys"         [ "$(q '.[] | select(.task=="t-old" and .arm=="oracle") | .skipped')" = no-oracle ]
check "none arm saw no .nugit store and no nugit CLAUDE.md"   bash -c "! cat $FAKE_CLAUDE_LOGDIR/ab-t-sub-none-*.log | grep -qE 'SAW_NUGIT_STORE|SAW_NUGIT_IN_CLAUDE_MD'"
check "nugit arm kept the store"                              bash -c "cat $FAKE_CLAUDE_LOGDIR/ab-t-sub-nugit-*.log | grep -q SAW_NUGIT_STORE"
check "every arm denied network tools identically"           [ "$(cat "$FAKE_CLAUDE_LOGDIR"/*.log | grep -c '^WebFetch$')" = 7 ]
check "allow rules with spaces reach claude as one argument" bash -c "grep -qx 'Bash(python3 -m pytest:\*)' $FAKE_CLAUDE_LOGDIR/ab-t-sub-none-*.log"
check "nugit arm may call the nugit tool headlessly"          bash -c "grep -qx 'mcp__nugit__context' $FAKE_CLAUDE_LOGDIR/ab-t-sub-nugit-*.log"
check "control arm is not granted the nugit tool"             bash -c "! grep -qx 'mcp__nugit__context' $FAKE_CLAUDE_LOGDIR/ab-t-sub-none-*.log"
check "nugit arm: context() call counted from the clone log"  [ "$(q '.[] | select(.task=="t-sub" and .arm=="nugit") | .context_calls')" -ge 1 ]
check "no control run contaminated"                           [ "$(q '[.[] | select(.arm != "nugit" and .contaminated == true)] | length')" = 0 ]
check "re-proposal heuristic flags the control"               [ "$(q '.[] | select(.task=="t-sub" and .arm=="none") | .reproposed_heuristic')" = true ]
check "re-proposal heuristic clears the oracle arm"           [ "$(q '.[] | select(.task=="t-sub" and .arm=="oracle") | .reproposed_heuristic')" = false ]
check "file_overlap = 1 (calc.go vs calc.go; tests excluded)" [ "$(q '.[] | select(.task=="t-sub" and .arm=="none") | .file_overlap')" = 1 ]
check "tags carried into results"                             [ "$(q '.[] | select(.task=="t-sub" and .arm=="dump") | .tags.kind')" = feat ]
check "committed + agent cost/tokens recorded"                [ "$(q '.[] | select(.task=="t-sub" and .arm=="none") | (.committed and .cost_usd == 0.0123 and .tokens.output == 50)')" = true ]
check "clones deleted (no -k)"                                bash -c "[ -z \"\$(ls -A $T/work 2>/dev/null)\" ]"
check "source repo has no leftover temp refs"                 [ -z "$(git -C "$R" for-each-ref refs/heads/agent-ab-tmp)" ]

check "leak check detects a reachable commit (negative case)" [ "$("$AB/clone-at.sh" "$R" "$SOL" "$T/neg" eval/neg "$BASE")" = "leak_check=fail" ]

echo "# summarize"
"$AB/summarize.sh" "$RES" > "$T/summary.txt" 2>&1 || bad "summarize.sh exited non-zero"
sed 's/^/    /' "$T/summary.txt"
check "summary has the paired table"                          grep -q "paired vs none" "$T/summary.txt"
check "summary: oracle beats none on t-sub (1 win)"           bash -c "grep -E '^all +oracle +1 +1 +0 +0' $T/summary.txt >/dev/null"
check "summary warns on small strata"                         grep -q "hint, not result" "$T/summary.txt"

echo "# tests visible (-T)"
OUT_T="$T/out-tv"
"$AB/run.sh" -t "$T/tasks.json" -r "$R" -b "$NUGIT" -o "$OUT_T" -w "$T/work" -a none -l 1 -T 2> "$T/tv.stderr" || { cat "$T/tv.stderr"; bad "run.sh -T exited non-zero"; }
qt() { jq -rs "$1" "$OUT_T/results.jsonl"; }
check "-T: tests_visible recorded"                            [ "$(qt '.[0].tests_visible')" = true ]
check "-T: the agent's prompt names the acceptance tests"     bash -c "cat $FAKE_CLAUDE_LOGDIR/ab-t-sub-none-*.log | grep -q 'Acceptance tests for this task are already committed: calc/sub_test.go'"
check "-T: the committed tests are not scored as agent work"  bash -c "! grep -q 'sub_test.go' $OUT_T/runs/t-sub-none-1/diff.patch"

echo "# quota guard"
before=$(wc -l < "$RES"); rc=0
FAKE_QUOTA_5H=90 "$AB/run.sh" -t "$T/tasks.json" -r "$R" -b "$NUGIT" -o "$OUT" -w "$T/work" -a none 2> "$T/guard.stderr" || rc=$?
check "guard exits 0"                                         [ "$rc" = 0 ]
check "guard printed its reason"                              grep -q "quota guard: five_hour used 90" "$T/guard.stderr"
check "guard added no results"                                [ "$(wc -l < "$RES")" = "$before" ]

echo
if [ "$fails" -eq 0 ]; then echo "smoke: all checks passed"; else echo "smoke: $fails check(s) FAILED"; exit 1; fi
