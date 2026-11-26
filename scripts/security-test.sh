#!/usr/bin/env bash
#
# Runs the security regressions by name, and fails when a name matches no test.
#
# `go test -run` with no match in a package prints "no tests to run" and exits 0, so renaming a
# guard would drop it from this gate without failing anything.
set -uo pipefail

cd "$(dirname "$0")/.."

packages=(./internal/serve ./internal/customql ./internal/store ./cmd/codeql-abom)
prefixes=(
	TestHTTPBoundary
	TestQueryLifecycle
	TestDeployedSuite
	TestEveryAPIMutation
	TestARequestTheBoundary
	TestAControlWhose
	TestOnlyTheDecisionPointWrites
	TestEveryCommand
	TestEveryDecidingCommand
	TestEveryEntryPoint
)

pattern="^($(IFS='|'; echo "${prefixes[*]}"))"
out=$(mktemp)
trap 'rm -f "$out"' EXIT

go test "${packages[@]}" -run "$pattern" -count=1 -v >"$out" 2>&1
status=$?
if [ $status -ne 0 ]; then
	grep -E '^(--- FAIL|FAIL|panic:|\s+\S+_test\.go:[0-9]+:)' "$out"
	echo "security-test: failed"
	exit $status
fi

missing=0
for prefix in "${prefixes[@]}"; do
	if ! grep -qE "^--- PASS: ${prefix}" "$out"; then
		echo "security-test: no test named ${prefix}* ran; a guard was renamed or removed"
		missing=1
	fi
done
[ $missing -eq 0 ] || exit 1
echo "security-test: $(grep -cE '^--- PASS: ' "$out") guards passed"
