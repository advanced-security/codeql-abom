#!/usr/bin/env bash
# Records what GitHub's code scanning holds for one of the operator's test repositories: its actions
# database and the analysis of the same commit, exactly as the API serves them.
#
# Read only, and no CodeQL runs here, so a crawl in flight does not matter. The repository runs
# default setup with the `remote` threat model, which is what lets its analysis stand in for ABOM's.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

REPO=${REPO:-has-ghas/test-repo-3}
TARGET=testdata/codescanning/${REPO/\//__}
mkdir -p "$TARGET"

commit=$(gh api "repos/$REPO/code-scanning/codeql/databases/actions" --jq .commit_oid)
id=$(gh api "repos/$REPO/code-scanning/analyses?tool_name=CodeQL&per_page=100" \
    --jq "[.[] | select(.commit_sha == \"$commit\" and (.category | contains(\"language:actions\")))][0].id")
if [[ -z "$id" || "$id" == "null" ]]; then
    echo "no actions analysis of $REPO at $commit, the commit its database was built from" >&2
    exit 1
fi

gh api -H "Accept: application/zip" "repos/$REPO/code-scanning/codeql/databases/actions" >"$TARGET/database.zip"
gh api -H "Accept: application/sarif+json" "repos/$REPO/code-scanning/analyses/$id" >"$TARGET/analysis.sarif"

echo "recorded $REPO at $commit and analysis $id; tests name both, so update them if either moved"
