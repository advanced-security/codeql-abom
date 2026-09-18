#!/usr/bin/env bash
# Regenerates the checked-in corpus fixtures from the source trees beside them.
#
# The expensive, non-deterministic half of building a corpus is CodeQL, and it needs the CLI and
# several seconds per repository. Recording its *output* is what makes `make corpus` pure Go,
# offline and fast — and what lets CI hold a populated database at all.
#
# Never hand-edit what this writes. Change a source tree under testdata/corpus and run this.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
corpus="$root/testdata/corpus"
findings="advanced-security/abom-actions-findings-src:codeql-suites/abom-experimental-findings.qls"

cd "$root"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT

while IFS= read -r tree; do
  repo="${tree#"$corpus"/}"
  slug="${repo//\//__}"
  out="$corpus/fixtures/$slug"
  mkdir -p "$out"
  echo "corpus: $repo"

  db="$scratch/$slug"
  codeql database create "$db" --language=actions --source-root="$tree" --overwrite >/dev/null 2>&1

  # The edges query directly rather than through the graph suite: ingest reads one result set,
  # and the suite also carries the surface and step-condition queries.
  codeql query run --database="$db" --additional-packs=ql \
    --output="$scratch/$slug.bqrs" ql/actions/graph-extraction/src/AbomEdges.ql >/dev/null 2>&1
  codeql bqrs decode --format=json --output="$out/edges.json" "$scratch/$slug.bqrs"

  # The experimental suite, so every job and composite action carries a finding and a clean
  # repository is distinguishable from one nobody analyzed.
  codeql database analyze "$db" --additional-packs=ql --format=sarif-latest \
    --output="$out/findings.sarif" --rerun -- "$findings" >/dev/null 2>&1

  # The CLI records the absolute path of every pack it resolved and stamps the run with a
  # wall-clock time. Both are machine-specific, and a committed fixture carrying them shows a
  # diff nobody made.
  python3 "$root/scripts/normalize-sarif-fixture.py" "$out/findings.sarif"
done < <(find "$corpus" -mindepth 2 -maxdepth 2 -type d -not -path "*/fixtures/*" | sort)

echo "corpus: fixtures written under $corpus/fixtures"
