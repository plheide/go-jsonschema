#!/usr/bin/env bash

# Installs the fork's pre-push guard (scripts/fork/pre-push) into this clone's
# hooks directory, which honours core.hooksPath. An existing pre-push hook that
# is not the guard is left alone, so no other check is lost.

set -o errexit -o nounset -o pipefail

root=$(git rev-parse --show-toplevel)
hooks=$(git rev-parse --git-path hooks)
target="${hooks}/pre-push"

if [[ -e "${target}" ]] && ! grep -qF 'installed by scripts/fork/install-hooks.sh' "${target}"; then
  echo "install-hooks: ${target} exists and is not the fork's guard; leaving it alone" >&2
  echo "install-hooks: move it aside, or call scripts/fork/pre-push from it, then run this again" >&2
  exit 1
fi

mkdir -p "${hooks}"
cp "${root}/scripts/fork/pre-push" "${target}"
chmod +x "${target}"

echo "installed ${target}"
