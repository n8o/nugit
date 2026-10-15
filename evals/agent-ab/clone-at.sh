#!/usr/bin/env bash
# clone-at.sh <source-repo> <commit> <dir> <branch> [<forbidden-commit>]
#
# Make a LEAK-FREE workspace for one replay run: a standalone clone of
# <source-repo> whose history ends at <commit>, checked out on <branch>.
#
# Why not `git worktree add`: a linked worktree shares the source repo's object
# database, so an agent working in it can `git show <solution>` and read the
# future. A clone made with --no-local copies only the objects reachable from
# the one ref it fetched, so a later commit is simply not there.
#
#   1. a temp ref at <commit> in the source repo (the only ref the clone sees);
#   2. git clone --no-local --no-tags --single-branch --branch <temp-ref>;
#   3. the temp ref is deleted, the clone's `origin` remote is removed and its
#      reflog (which names the source path) is dropped;
#   4. with <forbidden-commit>, `git cat-file -e <forbidden>` must FAIL in the
#      clone. Prints `leak_check=pass|fail` (or `leak_check=n/a`) on stdout; the
#      caller decides what a fail means (run.sh aborts the batch).
set -euo pipefail
repo=${1:?usage: clone-at.sh <repo> <commit> <dir> <branch> [<forbidden>]}
commit=${2:?} dir=${3:?} branch=${4:?} forbid=${5:-}

sha=$(git -C "$repo" rev-parse --verify --quiet "$commit^{commit}") || {
  echo "clone-at: $commit is not a commit in $repo" >&2; exit 2; }
tmp="agent-ab-tmp/$$-$RANDOM-$(date +%s)"
git -C "$repo" update-ref "refs/heads/$tmp" "$sha"
cleanup() { git -C "$repo" update-ref -d "refs/heads/$tmp" 2>/dev/null || true; }
trap cleanup EXIT

rm -rf "$dir"
mkdir -p "$(dirname "$dir")"
git clone -q --no-local --no-tags --single-branch --branch "$tmp" -- "$repo" "$dir"
cleanup
git -C "$dir" remote remove origin
git -C "$dir" branch -q -m "$tmp" "$branch"
rm -rf "$dir/.git/logs"
# Local identity so the agent's and the harness's commits never depend on the
# operator's global config.
git -C "$dir" config user.name "agent-ab"
git -C "$dir" config user.email "agent-ab@example.invalid"

if [ -n "$forbid" ]; then
  fsha=$(git -C "$repo" rev-parse --verify --quiet "$forbid^{commit}" || echo "$forbid")
  if [ "$fsha" = "$sha" ]; then
    echo "leak_check=fail"   # the forbidden commit IS the base: nothing to hide
  elif git -C "$dir" cat-file -e "$fsha^{commit}" 2>/dev/null; then
    echo "leak_check=fail"
  else
    echo "leak_check=pass"
  fi
else
  echo "leak_check=n/a"
fi
