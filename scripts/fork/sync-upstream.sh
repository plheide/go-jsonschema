#!/usr/bin/env bash

# Fast-forwards upstream-main to upstream's main, locally and on the fork.
#
#   scripts/fork/sync-upstream.sh [--dry-run]
#
# main is never touched here: it takes upstream-main in through a pull
# request. upstream-main only mirrors upstream, so anything but a
# fast-forward is refused.

set -o errexit -o nounset -o pipefail

upstream=${UPSTREAM_REMOTE:-upstream}
fork=${FORK_REMOTE:-origin}
push_args=()

# Exactly no argument or --dry-run: a mistyped flag must not push for real.
case "$#:${1:-}" in
  0:) ;;
  1:--dry-run) push_args+=(--dry-run) ;;
  *)
    echo "usage: scripts/fork/sync-upstream.sh [--dry-run]" >&2
    exit 2
    ;;
esac

git fetch "${upstream}" main
git fetch "${fork}" upstream-main

if ! git merge-base --is-ancestor "refs/remotes/${fork}/upstream-main" "refs/remotes/${upstream}/main"; then
  echo "sync-upstream: ${fork}/upstream-main is not an ancestor of ${upstream}/main; refusing" >&2
  exit 1
fi

if [[ ${#push_args[@]} -eq 0 ]]; then
  # Fast-forwards the local branch too; refuses if it has moved on its own.
  # git fetch won't update the checked-out branch, so merge there instead.
  current=$(git branch --show-current)
  if [[ "${current}" == "upstream-main" ]]; then
    git merge --ff-only "refs/remotes/${upstream}/main"
  else
    git fetch . "refs/remotes/${upstream}/main:refs/heads/upstream-main"
  fi
fi

# The guarded expansion: bash before 4.4 treats an empty "${push_args[@]}" as
# unbound under nounset.
git push ${push_args[@]+"${push_args[@]}"} "${fork}" "refs/remotes/${upstream}/main:refs/heads/upstream-main"
