#!/usr/bin/env bash
# Lints this repository's workflows, and the ones the query repository template installs, with the
# pinned actionlint. actionlint runs shellcheck over every `run:` only when shellcheck is present, so
# a missing one is a failure: otherwise a developer lints less than CI and both call it clean.
set -euo pipefail

want=${ACTIONLINT_VERSION:?set ACTIONLINT_VERSION}
install="go install github.com/rhysd/actionlint/cmd/actionlint@$want"
if ! command -v actionlint >/dev/null 2>&1; then
	echo "actionlint is not installed: $install"
	exit 1
fi
have=$(actionlint -version | sed -n 1p)
if [ "${have#v}" != "${want#v}" ]; then
	echo "actionlint is $have, not $want: $install"
	exit 1
fi
if ! command -v shellcheck >/dev/null 2>&1; then
	echo "shellcheck is not installed: brew install shellcheck, or apt-get install shellcheck"
	exit 1
fi

config="$PWD/.github/actionlint.yaml"
actionlint -config-file "$config"

# Where they will live, in a repository of their own, so the template's call to its own reusable
# workflow resolves: actionlint finds a project by its Git directory.
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
git init -q "$stage"
mkdir -p "$stage/.github/workflows"
cp infra/qlaas-repo-template/workflows/*.yml "$stage/.github/workflows/"
if ! (cd "$stage" && actionlint -config-file "$config"); then
	echo "workflows: the query repository template's, under infra/qlaas-repo-template/workflows"
	exit 1
fi
echo "workflows: clean"
