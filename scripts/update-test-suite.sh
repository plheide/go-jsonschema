#!/bin/sh

# Vendors the draft-07 tests of the JSON-Schema-Test-Suite, at a commit, into
# tests/testdata/JSON-Schema-Test-Suite: the required tests (optional/ is left
# out), the remotes they refer to, the suite's license, and the commit.
#
# Usage: scripts/update-test-suite.sh <commit>

set -e
set -o errexit -o nounset

commit=${1:?usage: scripts/update-test-suite.sh <commit>}
dest=tests/testdata/JSON-Schema-Test-Suite
tmp=$(mktemp -d)
trap 'rm -rf "${tmp}"' EXIT

git clone --quiet --filter=blob:none https://github.com/json-schema-org/JSON-Schema-Test-Suite.git "${tmp}/suite"
git -C "${tmp}/suite" checkout --quiet "${commit}"

rm -rf "${dest}"
mkdir -p "${dest}/tests/draft7" "${dest}/remotes"
cp "${tmp}/suite/LICENSE" "${dest}/"
cp "${tmp}/suite"/tests/draft7/*.json "${dest}/tests/draft7/"

# The remotes draft-07 tests resolve: the draft-agnostic ones, and draft7's own.
cp "${tmp}/suite"/remotes/*.json "${dest}/remotes/"
for dir in baseUriChange baseUriChangeFolder baseUriChangeFolderInSubschema draft7 nested; do
	cp -R "${tmp}/suite/remotes/${dir}" "${dest}/remotes/"
done

git -C "${tmp}/suite" log -1 --format='%H %cs' >"${dest}/COMMIT"
