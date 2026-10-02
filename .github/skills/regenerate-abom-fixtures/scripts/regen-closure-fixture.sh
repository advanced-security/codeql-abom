#!/usr/bin/env bash
# Regenerates the closure-database fixtures from the checked-in staged tree.
#
# The tree is laid out exactly as `closure.Layout.Write` stages a closure: the owned repository's
# own files at the root, composites under .github/actions/external/, reusable workflows under
# .github/workflows/external/. It is built with the index include the closure appends, because
# `codeql test run` ignores index filters and cannot show what `database create` indexes.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

ABOM_TMP=${ABOM_TMP:-$PWD/.abom}
SCRATCH=$ABOM_TMP/fixtures/closure
mkdir -p "$SCRATCH"

DB=${DB:-$SCRATCH/closure-db}
SRC=ql/actions/graph-extraction/test/ResolvedCallees
SUITE=advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls
TARGET=testdata/closure

# Every codeql invocation shares one local compilation cache, so a crawl in flight contends for
# it and for every core. Refuse rather than produce a fixture nobody should trust.
status=$(curl -sS --max-time 2 "http://${ABOM_ADDR:-127.0.0.1:8099}/api/discovery/status" 2>/dev/null || true)
if [[ -n "$status" && "$status" != *'"state":"stopped"'* ]]; then
    echo "discovery is not stopped; pause it first (make stop, or the Discovery page)" >&2
    exit 1
fi

rm -rf "$DB"
LGTM_INDEX_FILTERS=$'include:.github/workflows/external/**/*.yml\ninclude:.github/workflows/external/**/*.yaml' \
    codeql database create --language=actions --source-root="$SRC" "$DB"

# The flags runner.AnalyzeQueries passes, so the fixture is what a closure run reads.
codeql database analyze "$DB" "$SUITE" --additional-packs ql \
    --format=sarif-latest --sarif-include-query-help=always --sarif-add-snippets \
    --no-sarif-add-file-contents --download --output="$SCRATCH/closure-findings.sarif"

# Each repository's own regular database, built from its files where they sit in that repository:
# the crawl's record a closure is built from, and the findings it is measured against.
regular() {
    local name=$1 tree=$SCRATCH/regular/$1
    rm -rf "$tree" "$SCRATCH/regular-db"
    mkdir -p "$tree"
    shift
    while (($#)); do
        mkdir -p "$tree/$(dirname "$2")"
        cp "$SRC/$1" "$tree/$2"
        shift 2
    done
    codeql database create --language=actions --source-root="$tree" "$SCRATCH/regular-db"
    codeql query run --database="$SCRATCH/regular-db" --output="$SCRATCH/regular-$name.bqrs" \
        ql/actions/graph-extraction/src/AbomEdges.ql
    codeql bqrs decode --format=json --output="$SCRATCH/regular-$name.json" "$SCRATCH/regular-$name.bqrs"
    codeql database analyze "$SCRATCH/regular-db" "$SUITE" --additional-packs ql \
        --format=sarif-latest --sarif-include-query-help=always --sarif-add-snippets \
        --no-sarif-add-file-contents --download --output="$SCRATCH/regular-$name.sarif"
}
regular octo-app .github/workflows/pr.yml .github/workflows/pr.yml \
    .github/actions/local/action.yml .github/actions/local/action.yml
regular vendor-comp .github/actions/external/vendor/comp/action.yml action.yml
regular vendor-inner .github/actions/external/vendor/inner/action.yml action.yml
regular vendor-rw .github/workflows/external/vendor/rw/.github/workflows/reuse.yml \
    .github/workflows/reuse.yml

mkdir -p "$TARGET"
cp "$SCRATCH/closure-findings.sarif" "$TARGET/findings.sarif"
for name in octo-app vendor-comp vendor-inner vendor-rw; do
    cp "$SCRATCH/regular-$name.json" "$TARGET/regular-$name-edges.json"
    cp "$SCRATCH/regular-$name.sarif" "$TARGET/regular-$name-findings.sarif"
done
echo "wrote $TARGET/"

echo
echo "Now read the diff before committing: a fixture change is a change to what the tests mean."
git --no-pager status --short -- "$TARGET"
