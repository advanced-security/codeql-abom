#!/usr/bin/env bash
# Regenerates the BQRS edge fixtures from the checked-in CodeQL test source tree.
#
# Three packages hold byte-identical copies of this fixture. Regenerating one by hand drifts the
# other two, and the tests that read them start disagreeing for no visible reason.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

DB=${DB:-/tmp/abom-fixture-db}
SRC=ql/actions/graph-extraction/test/AbomEdges
QUERY=ql/actions/graph-extraction/src/AbomEdges.ql
TARGETS=(internal/bqrs internal/expand internal/ingest)

# Every codeql invocation shares one local compilation cache, so a crawl in flight contends for
# it and for every core. Refuse rather than produce a fixture nobody should trust.
status=$(curl -sS --max-time 2 "http://${ABOM_ADDR:-127.0.0.1:8099}/api/discovery/status" 2>/dev/null || true)
if [[ -n "$status" && "$status" != *'"state":"stopped"'* ]]; then
    echo "discovery is not stopped; pause it first (make stop, or the Discovery page)" >&2
    exit 1
fi

# codeql refuses to write into an existing directory, which re-running would otherwise hit.
rm -rf "$DB"
codeql database create --language=actions --source-root="$SRC" "$DB"
codeql query run --database="$DB" --output=/tmp/abom-edges.bqrs "$QUERY"
codeql bqrs decode --format=json --output=/tmp/abom-edges.json /tmp/abom-edges.bqrs

for target in "${TARGETS[@]}"; do
    cp /tmp/abom-edges.json "$target/testdata/abom-edges.json"
    echo "wrote $target/testdata/abom-edges.json"
done

echo
echo "Now read the diff before committing: a fixture change is a change to what the tests mean."
git --no-pager diff --stat -- '*/testdata/abom-edges.json'
