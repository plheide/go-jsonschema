#!/usr/bin/env bash

# Merges a fork pull request into main, refusing anything unsafe.
#
#   scripts/fork/merge-layer.sh <pr-number>
#
# Refuses unless the pull request targets main, its head matches the local
# branch of the same name, GitHub reports it CLEAN, and the repository does
# not delete head branches on merge. Merges with a merge commit pinned to that
# head, and never deletes the branch: deleting a feat/* branch would close the
# upstream pull request it heads.

set -o errexit -o nounset -o pipefail

repo=${FORK_REPO:-plheide/go-jsonschema}
pr=${1:?usage: merge-layer.sh <pr-number>}

fields=$(gh pr view "${pr}" --repo "${repo}" --json baseRefName,headRefName,headRefOid,mergeStateStatus \
  --jq '"\(.baseRefName) \(.headRefName) \(.headRefOid) \(.mergeStateStatus)"')
read -r base head oid state <<< "${fields}"

if [[ "${base}" != main ]]; then
  echo "merge-layer: #${pr} targets ${base}, not main" >&2
  exit 1
fi

if [[ "${state}" != CLEAN ]]; then
  echo "merge-layer: #${pr} is ${state}, not CLEAN" >&2
  exit 1
fi

local_oid=$(git rev-parse --verify --quiet "refs/heads/${head}" || true)
if [[ "${local_oid}" != "${oid}" ]]; then
  echo "merge-layer: #${pr} heads ${oid}, but local ${head} is ${local_oid:-missing}" >&2
  exit 1
fi

# The merge below keeps the branch, but GitHub's "Automatically delete head
# branches" setting would delete it anyway.
auto_delete=$(gh api "repos/${repo}" --jq '.delete_branch_on_merge')
if [[ "${auto_delete}" != false ]]; then
  echo "merge-layer: ${repo} deletes head branches on merge (delete_branch_on_merge=${auto_delete}); turn that off first" >&2
  exit 1
fi

gh pr merge "${pr}" --repo "${repo}" --merge --match-head-commit "${oid}"
