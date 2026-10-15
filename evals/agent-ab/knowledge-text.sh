#!/usr/bin/env bash
# knowledge-text.sh oracle <repo> <base> <outfile> <key>...
# knowledge-text.sh dump   <repo> <base> <outfile> [<cap-chars>]
#
# Build the text the oracle and dump arms receive through --append-system-prompt.
# Everything is read from the store AT BASE (`git show <base>:<path>`), never
# from the working tree, so neither arm sees knowledge written after the task.
#
#   oracle: the exact files whose front-matter `id:` (or `key:`) is one of the
#           task's oracle_keys, anywhere under .nugit/.
#   dump:   every EFFECTIVE decision under .nugit/decisions — status not
#           superseded/deprecated/rejected and not named by another file's
#           `supersedes:` (ADR-0003) — whole files in path order until the next
#           file would cross <cap-chars> (default 120000).
#
# Writes the text (header + files) to <outfile> and prints one JSON object of
# metadata: oracle → {chars, files, found, missing}; dump → {chars, files,
# truncated}. "chars" is bytes of the written text.
set -euo pipefail
mode=${1:?usage: knowledge-text.sh oracle|dump <repo> <base> <outfile> ...}
repo=${2:?} base=${3:?} out=${4:?}
shift 4

ORACLE_HEADER="## Project decision records for this task
The records below are this repository's own decisions that bear on the task
(from its decision store, as of the commit you are working on). Follow them;
their Rejected sections list alternatives the project already turned down."
DUMP_HEADER="## Project decision records
Below are all of this repository's current decision records (from its decision
store, as of the commit you are working on). Follow the ones that bear on your
task; their Rejected sections list alternatives the project already turned down."

append_file() { # path
  printf '\n\n----- %s -----\n' "$1" >> "$out"
  git -C "$repo" show "$base:$1" >> "$out"
}

case $mode in
oracle)
  printf '%s\n' "$ORACLE_HEADER" > "$out"
  found=() missing=() files=()
  for key in "$@"; do
    [ -n "$key" ] || continue
    # git grep at a tree prints "<base>:<path>"; strip the "<base>:" prefix.
    hit=$(git -C "$repo" grep -l -E "^(id|key):[[:space:]]*[\"']?${key}[\"']?[[:space:]]*\$" "$base" -- .nugit 2>/dev/null \
          | head -1 | sed "s|^$base:||" || true)
    if [ -n "$hit" ]; then
      append_file "$hit"; found+=("$key"); files+=("$hit")
    else
      missing+=("$key")
    fi
  done
  chars=$(wc -c < "$out" | tr -d ' ')
  [ ${#found[@]} -gt 0 ] || chars=0
  jq -nc --argjson chars "$chars" \
    --arg found "${found[*]:-}" --arg missing "${missing[*]:-}" --arg files "${files[*]:-}" \
    '{chars:$chars, files:($files|split(" ")|map(select(.!=""))|length),
      found:($found|split(" ")|map(select(.!=""))), missing:($missing|split(" ")|map(select(.!="")))}'
  ;;
dump)
  cap=${1:-120000}
  printf '%s\n' "$DUMP_HEADER" > "$out"
  # front-matter fields per file: "id <x>", "status <x>", "supersedes <x>"
  fm() {
    git -C "$repo" show "$base:$1" | awk '
      NR==1 && /^---[ \t\r]*$/ { fm=1; next }
      !fm { exit }
      /^---[ \t\r]*$/ { exit }
      /^[^ \t]/ { insup=0 }
      /^id:/     { v=$0; sub(/^id:[ \t]*/,"",v); gsub(/["\047\r]/,"",v); print "id " v; next }
      /^status:/ { v=$0; sub(/^status:[ \t]*/,"",v); gsub(/["\047\r ]/,"",v); print "status " v; next }
      /^supersedes:/ { insup=1; v=$0; sub(/^supersedes:[ \t]*/,"",v); gsub(/[][\"\047 \r]/,"",v)
                       n=split(v,a,","); for(i=1;i<=n;i++) if(a[i]!="") print "supersedes " a[i]; next }
      insup && /^[ \t]+-/ { v=$0; sub(/^[ \t]+-[ \t]*/,"",v); gsub(/["\047 \r]/,"",v); print "supersedes " v }'
  }
  paths=$(git -C "$repo" ls-tree -r --name-only "$base" -- .nugit/decisions 2>/dev/null | grep '\.md$' || true)
  superseded=" "
  meta=""
  for p in $paths; do
    m=$(fm "$p")
    meta+="$p"$'\t'"$(awk '$1=="id"{print $2}' <<<"$m" | head -1)"$'\t'"$(awk '$1=="status"{print $2}' <<<"$m" | head -1)"$'\n'
    superseded+="$(awk '$1=="supersedes"{printf "%s ", $2}' <<<"$m")"
  done
  n=0 truncated=false
  while IFS=$'\t' read -r p id status; do
    [ -n "$p" ] || continue
    case $status in superseded|deprecated|rejected) continue ;; esac
    [ -n "$id" ] && case $superseded in *" $id "*) continue ;; esac
    size=$(git -C "$repo" cat-file -s "$base:$p")
    cur=$(wc -c < "$out" | tr -d ' ')
    if [ $((cur + size + ${#p} + 16)) -gt "$cap" ]; then truncated=true; break; fi
    append_file "$p"; n=$((n + 1))
  done <<<"$meta"
  chars=$(wc -c < "$out" | tr -d ' ')
  [ "$n" -gt 0 ] || chars=0
  jq -nc --argjson chars "$chars" --argjson files "$n" --argjson truncated "$truncated" \
    '{chars:$chars, files:$files, truncated:$truncated}'
  ;;
*) echo "knowledge-text: unknown mode $mode" >&2; exit 2 ;;
esac
