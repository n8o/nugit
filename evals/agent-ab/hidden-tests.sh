#!/usr/bin/env bash
# hidden-tests.sh <dir> <verify-cmd> [<test-file>...]
#
# Run a task's hidden tests in <dir> (the files must already be in place) and
# print one JSON object: {result, exit, command}.
#
#   result: pass | fail | unscored | none
#     - <verify-cmd> non-empty → run it as-is (it is the task author's command);
#     - else derive one per language from the test files' extensions:
#         *.go                         → go test -count=1 -run '^(TestX|...)$' ./<pkg>
#                                        run from the nearest go.mod (nested
#                                        modules work), only the Test funcs the
#                                        hidden files declare (a whole package
#                                        can need services the clone lacks)
#         *.test|spec.{ts,tsx,js,jsx}  → npx vitest run <files> when "vitest"
#                                        appears in package.json, else npx jest <files>
#         *.py                         → python3 -m pytest <files>
#       Several languages → every group must pass. Files of any other kind are
#       ignored when at least one group is runnable, else → unscored.
#     - no verify and no files → none.
#
# The full test output goes to $HT_LOG (default /dev/null). Each command is
# bounded by $HT_TIMEOUT seconds (default 900) when `timeout`/`gtimeout` exists.
set -uo pipefail
dir=${1:?usage: hidden-tests.sh <dir> <verify> [files...]}
verify=${2:-}
shift 2 || shift $#
log=${HT_LOG:-/dev/null}
tmo=${HT_TIMEOUT:-900}
TO=""
if command -v timeout >/dev/null 2>&1; then TO="timeout $tmo"
elif command -v gtimeout >/dev/null 2>&1; then TO="gtimeout $tmo"; fi

emit() { jq -nc --arg r "$1" --argjson e "$2" --arg c "$3" '{result:$r, exit:$e, command:$c}'; }

cmds=()
if [ -n "$verify" ]; then
  cmds+=("$verify")
elif [ $# -eq 0 ]; then
  emit none null ""; exit 0
else
  go_cmds=() js_files="" py_files=""
  for f in "$@"; do
    case $f in
      *_test.go)
            d=$(dirname "$f"); m=$d
            while [ "$m" != . ] && [ ! -f "$dir/$m/go.mod" ]; do m=$(dirname "$m"); done
            if [ "$d" = "$m" ]; then rel=.; elif [ "$m" = . ]; then rel="./$d"; else rel="./${d#"$m"/}"; fi
            fns=$(grep -hoE '^func (Test[A-Za-z0-9_]+)\(' "$dir/$f" 2>/dev/null | sed -E 's/^func ([^(]+)\(/\1/' | paste -sd'|' -)
            if [ -n "$fns" ]; then
              go_cmds+=("cd '$m' && go test -count=1 -run '^($fns)\$' $rel")
            else
              go_cmds+=("cd '$m' && go test -count=1 $rel")
            fi ;;
      *.test.ts|*.test.tsx|*.test.js|*.test.jsx|*.spec.ts|*.spec.tsx|*.spec.js|*.spec.jsx)
            js_files+=" $f" ;;
      *.py) py_files+=" $f" ;;
    esac
  done
  [ ${#go_cmds[@]} -gt 0 ] && for c in "${go_cmds[@]}"; do case " ${cmds[*]-} " in *"$c"*) ;; *) cmds+=("$c") ;; esac; done
  if [ -n "$js_files" ]; then
    if [ -f "$dir/package.json" ] && grep -q '"vitest"' "$dir/package.json"; then
      cmds+=("npx vitest run$js_files")
    else
      cmds+=("npx jest$js_files")
    fi
  fi
  if [ -n "$py_files" ]; then
    py=python3; command -v python3 >/dev/null 2>&1 || py=python
    cmds+=("$py -m pytest$py_files")
  fi
  if [ ${#cmds[@]} -eq 0 ]; then emit unscored null ""; exit 0; fi
fi

result=pass code=0 joined=""
for c in "${cmds[@]}"; do
  joined+="${joined:+ && }$c"
  printf '$ %s\n' "$c" >> "$log"
  (cd "$dir" && $TO bash -c "$c" </dev/null) >> "$log" 2>&1
  rc=$?
  printf '[exit %s]\n' "$rc" >> "$log"
  if [ "$rc" -ne 0 ]; then result=fail; [ "$code" -eq 0 ] && code=$rc; fi
done
emit "$result" "$code" "$joined"
