#!/usr/bin/env bash
# repropose.sh <dir> <base> <rejected.json>
#
# HEURISTIC re-proposal flag — not a judgement. Did the agent's diff add code
# that names a rejected alternative? Prints {flag, alternatives, matched, terms}.
#
# <rejected.json> is the task's `rejected` array ([{key, text}]). For each
# text, the distinctive terms are the code-ish tokens in it:
#   - `backticked spans`
#   - CamelCase / camelCase identifiers   (a lower→upper transition)
#   - snake_case identifiers              (an underscore between word chars)
#   - dotted names                        (pkg.Func, a.b.c)
# kept only when ≥ 5 characters long and NOT already present (fixed-string) in
# the base version of the files the agent touched — a term the code already
# used is no evidence of re-proposal. An alternative is flagged when any of its
# terms appears in the diff's added lines (excluding .nugit/ and .beads/).
#
# flag: true / false, or null when there is nothing to check (no rejected
# alternatives, or none yielded a usable term). Expect both false positives (a
# term reused for an unrelated reason) and false negatives (a re-proposal
# written in different words); read the transcripts before quoting it.
set -euo pipefail
dir=${1:?usage: repropose.sh <dir> <base> <rejected.json>}
base=${2:?} rej=${3:?}

n=$(jq 'length' "$rej" 2>/dev/null || echo 0)
if [ "$n" -eq 0 ]; then jq -nc '{flag:null, alternatives:0, matched:0, terms:[]}'; exit 0; fi

added=$(git -C "$dir" diff "$base"..HEAD -- . ':(exclude).nugit' ':(exclude).beads' | grep '^+' | grep -v '^+++' || true)
basetext=$(git -C "$dir" diff --name-only "$base"..HEAD -- . ':(exclude).nugit' ':(exclude).beads' \
           | while read -r f; do git -C "$dir" show "$base:$f" 2>/dev/null || true; done)

terms_of() { # text on stdin → candidate terms, one per line
  local t; t=$(cat)
  {
    # shellcheck disable=SC2016 # literal backticks, not an expansion
    grep -oE '`[^`]+`' <<<"$t" | tr -d '`'
    grep -oE '[A-Za-z0-9]*[a-z][A-Z][A-Za-z0-9]*' <<<"$t"
    grep -oE '[A-Za-z0-9]+(_[A-Za-z0-9]+)+' <<<"$t"
    grep -oE '[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)+' <<<"$t"
  } | awk 'length($0) >= 5' | sort -u
}

usable=0 matched=0 hits=""
for i in $(seq 0 $((n - 1))); do
  any=0 hit=0
  while IFS= read -r term; do
    [ -n "$term" ] || continue
    grep -qF -- "$term" <<<"$basetext" && continue
    any=1
    if grep -qF -- "$term" <<<"$added"; then hit=1; hits+="$term"$'\n'; fi
  done < <(jq -r ".[$i].text // \"\"" "$rej" | terms_of)
  [ "$any" = 1 ] && usable=$((usable + 1))
  [ "$hit" = 1 ] && matched=$((matched + 1))
done

jq -nc --argjson n "$n" --argjson usable "$usable" --argjson matched "$matched" --arg hits "$hits" '
  {flag: (if $usable == 0 then null else ($matched > 0) end),
   alternatives: $n, matched: $matched,
   terms: ($hits | split("\n") | map(select(. != "")) | unique)}'
