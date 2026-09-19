#!/usr/bin/env bash
# Imports GitHub's own Actions query-test trees as fixture repositories.
#
# ABOM's shared data set is the org-level equivalent of a per-query unit test: instead of an
# `.expected` file per query, the whole set of findings is mapped onto one fixed dependency graph.
# For that to mean anything the set has to be able to *produce* each finding, and the trees that
# are known to produce them are the ones CodeQL tests its own queries with.
#
# Copied rather than referenced, because `make corpus` must stay offline and the recorded CodeQL
# output beside each tree is what a fixture is. Only `.github/` is taken: `.testproj`, `.expected`
# and `.qlref` belong to CodeQL's test runner and would be recorded as source.
#
# The source is a sparse checkout of github/codeql under .codeql/sparse-checkout, which is not
# vendored here. Absent, this reports what it needed and exits without touching anything.
#
#   ./scripts/import-actions-test-fixtures.sh          # copy anything new or changed
#   ./scripts/import-actions-test-fixtures.sh --list   # show the mapping and stop
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tests="$root/.codeql/sparse-checkout/actions/ql/test/query-tests"
corpus="$root/testdata/corpus"
owner="globex-industries"

# Which CodeQL test tree becomes which repository. The repository names are ordinary ones on
# purpose — the set is meant to read like an organization rather than like a test suite — so this
# table is the only record of where each came from, and of which rules it is here to exercise.
mapping=(
  "Security/CWE-074|release-tooling|expression injection into a run step"
  "Security/CWE-077|build-agents|command injection"
  "Security/CWE-078|deploy-scripts|OS command injection"
  "Security/CWE-088|cli-wrapper|argument injection"
  "Security/CWE-094|web-frontend|code injection, the largest set"
  "Security/CWE-1395|legacy-pipeline|a dependency with a known advisory"
  "Security/CWE-200|metrics-agent|secret exposure through logs"
  "Security/CWE-275|docs-site|missing workflow permissions"
  "Security/CWE-285|access-gateway|improper access control"
  "Security/CWE-312|config-service|secrets written in the clear"
  "Security/CWE-349|artifact-store|artifact poisoning"
  "Security/CWE-367|scheduler|checkout time-of-check to time-of-use"
  "Security/CWE-571|feature-flags|an if expression that is always true"
  "Security/CWE-829|mobile-app|untrusted checkout, the second largest set"
  "Security/CWE-829-Lockfile|dependency-lock|an unpinned lockfile action"
  "Security/CWE-829-untrusted-owner|partner-integrations|an action from an untrusted owner"
  "Security/CWE-918|proxy-edge|server-side request forgery"
  "Violations Of Best Practice/CodeQL|codeql-config|advanced configuration nobody needs"
  "Models|platform-tools|the model tests, for shapes the others do not carry"
)

if [ "${1:-}" = "--list" ]; then
  for row in "${mapping[@]}"; do
    IFS='|' read -r from repo why <<< "$row"
    printf '%-38s -> %s/%-24s %s\n' "$from" "$owner" "$repo" "$why"
  done
  exit 0
fi

if [ ! -d "$tests" ]; then
  echo "import: $tests is missing."
  echo "import: it is a sparse checkout of github/codeql's actions/ tree, not vendored here."
  echo "import: nothing copied; the recorded fixtures already under $corpus are unaffected."
  exit 0
fi

copied=0
for row in "${mapping[@]}"; do
  IFS='|' read -r from repo _ <<< "$row"
  src="$tests/$from/.github"
  if [ ! -d "$src" ]; then
    echo "import: $from has no .github tree; skipped"
    continue
  fi
  dst="$corpus/$owner/$repo"
  # Replaced rather than merged, so a file deleted upstream does not live on here forever.
  rm -rf "$dst"
  mkdir -p "$dst"
  cp -R "$src" "$dst/.github"
  # CodeQL's runner leaves build output inside the tree it tests; recording it as source would
  # put a database's own trap files into the graph.
  find "$dst" -name '*.testproj' -type d -prune -exec rm -rf {} + 2>/dev/null || true
  copied=$((copied + 1))
  echo "imported: $from -> $owner/$repo"
done

echo "imported $copied trees under $corpus/$owner"
echo "next: ./scripts/regenerate-corpus-fixtures.sh   # records CodeQL output for what changed"
