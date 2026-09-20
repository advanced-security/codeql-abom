#!/usr/bin/env bash
# Regenerates the checked-in fixtures from the source trees beside them.
#
# The expensive, non-deterministic half of building the data set is CodeQL, and it needs the CLI
# and several seconds per repository. Recording its *output* is what makes `make corpus` pure Go,
# offline and fast — and what lets CI hold a populated database at all.
#
# Incremental by default: a tree is rebuilt only when one of its files is newer than what was
# recorded from it. Rebuilding every tree costs a CodeQL database each, and almost every run
# changes one file, so the default was paying for the whole set to record one.
#
#   ./scripts/regenerate-corpus-fixtures.sh                  # only what changed
#   ./scripts/regenerate-corpus-fixtures.sh octo-org/app     # named trees, always
#   FORCE=1 ./scripts/regenerate-corpus-fixtures.sh          # everything
#
# Never hand-edit what this writes. Change a source tree under testdata/corpus and run this.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
corpus="$root/testdata/corpus"
# Both suites, because they answer different questions and a crawl with both tiers bound runs
# both. The default suite is what produces *security* findings — unpinned tags, missing
# permissions, untrusted checkout — and recording only the experimental one left every source
# tree reporting nothing but a coverage marker, however deliberately vulnerable it was written.
findings="advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"
marker="advanced-security/abom-actions-findings-src:codeql-suites/abom-experimental-findings.qls"

cd "$root"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

# The queries are as much an input as the source tree: AbomEdges.ql, AbomSurface.ql, both
# findings suites and the libraries and locks under them decide what a recording says. Watching
# only the tree left every artifact looking current after a query changed, so the checked-in
# output described behavior the pack no longer has.
inputs=("$root/ql/actions/graph-extraction/src" "$root/ql/actions/findings/src")

# stale reports whether a tree or the queries have changed since its output was recorded. Any
# missing artifact counts as stale, so adding a new recorded query regenerates everything once
# and no more.
stale() {
  local tree="$1" out="$2"
  [ -n "${FORCE:-}" ] && return 0
  local artifact
  for artifact in edges.json surface.json findings.sarif; do
    [ -f "$out/$artifact" ] || return 0
  done
  [ -n "$(find "$tree" "${inputs[@]}" -type f -newer "$out/edges.json" -print -quit)" ]
}

if [ "$#" -gt 0 ]; then
  trees=$(printf '%s\n' "$@" | sed "s|^|$corpus/|")
  export FORCE=1
else
  trees=$(find "$corpus" -mindepth 2 -maxdepth 2 -type d -not -path "*/fixtures/*" | sort)
fi

built=0
skipped=0
while IFS= read -r tree; do
  [ -n "$tree" ] || continue
  repo="${tree#"$corpus"/}"
  slug="${repo//\//__}"
  out="$corpus/fixtures/$slug"
  mkdir -p "$out"

  if ! stale "$tree" "$out"; then
    skipped=$((skipped + 1))
    continue
  fi
  echo "recording: $repo"
  built=$((built + 1))

  db="$scratch/$slug"
  codeql database create "$db" --language=actions --source-root="$tree" --overwrite >/dev/null 2>&1

  # The edges query directly rather than through the graph suite: ingest reads one result set,
  # and the suite also carries the step-condition query.
  codeql query run --database="$db" --additional-packs=ql \
    --output="$scratch/$slug.bqrs" ql/actions/graph-extraction/src/AbomEdges.ql >/dev/null 2>&1
  codeql bqrs decode --format=json --output="$out/edges.json" "$scratch/$slug.bqrs"

  # What a job can do, which nothing else records. Without it every job-level column the
  # interface renders — permissions, triggers, runner, privilege — is zero for every row, and a
  # column that is zero everywhere teaches a reader nothing about the data set.
  codeql query run --database="$db" --additional-packs=ql \
    --output="$scratch/$slug-surface.bqrs" \
    ql/actions/graph-extraction/src/AbomSurface.ql >/dev/null 2>&1
  codeql bqrs decode --format=json --output="$out/surface.json" "$scratch/$slug-surface.bqrs"

  # The experimental suite gives every job and composite action a marker, so a clean repository
  # is distinguishable from one nobody analyzed; the default suite gives the real findings.
  codeql database analyze "$db" --additional-packs=ql --format=sarif-latest \
    --output="$out/findings.sarif" --rerun -- "$findings" "$marker" >/dev/null 2>&1

  # The CLI records the absolute path of every pack it resolved and stamps the run with a
  # wall-clock time. Both are machine-specific, and a committed fixture carrying them shows a
  # diff nobody made.
  python3 "$root/scripts/normalize-sarif-fixture.py" "$out/findings.sarif"
done <<< "$trees"

echo "recorded $built, unchanged $skipped, under $corpus/fixtures"

# The rules the default suite can produce, recorded beside the fixtures so a Go test can ask what
# the data set fails to represent without needing the CodeQL CLI to resolve a suite.
rules="$corpus/suite-rules.txt"
codeql resolve queries "$findings" --additional-packs=ql 2>/dev/null | grep '\.ql$' | while read -r q; do
  grep -m1 '@id' "$q" 2>/dev/null | sed 's/.*@id *//'
done | sort -u > "$rules"
echo "recorded $(wc -l < "$rules" | tr -d ' ') suite rules in $rules"
