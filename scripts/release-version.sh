#!/usr/bin/env bash
#
# The release version, and the files that have to carry it.
#
#   release-version.sh --check <version>   fail unless this tree can be released as <version>
#   release-version.sh --name <version>    fail unless <version> names a release of the pinned CLI
#   release-version.sh <version>           set every pack to <version>, then check
#
# A release is vX.Y.Z, where X.Y.Z is the CodeQL CLI pinned in .codeql-version, or
# vX.Y.Z-<pre-release> for a further release against that same CLI, such as v2.27.1-next.1.
# Releasing never moves the CLI pin; update-codeql.yml does.
#
# SemVer orders v2.27.1-next.1 *before* v2.27.1, although it is made after it. Every pre-release is
# published as a GitHub pre-release and never as latest, so nothing may pick between releases by
# SemVer precedence.
set -euo pipefail

cd "$(dirname "$0")/.."

usage() {
	echo "usage: release-version.sh [--check | --name] <vMAJOR.MINOR.PATCH[-pre-release]>" >&2
	exit 2
}

mode=set
case "${1:-}" in
--check | --name)
	mode=${1#--}
	shift
	;;
esac
[ $# -eq 1 ] || usage

tag=$1
case "$tag" in
v*) ;;
*) tag="v$tag" ;;
esac
semver=${tag#v}

if ! [[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
	echo "release-version: $tag is not vMAJOR.MINOR.PATCH with an optional pre-release" >&2
	exit 1
fi
if [[ "$semver" == *-* ]]; then
	IFS=. read -ra identifiers <<<"${semver#*-}"
	for id in "${identifiers[@]}"; do
		if [[ "$id" =~ ^0[0-9]+$ ]]; then
			echo "release-version: $tag has a numeric pre-release identifier with a leading zero" >&2
			exit 1
		fi
	done
fi
if ! git check-ref-format "refs/tags/$tag"; then
	echo "release-version: $tag is not a valid tag name" >&2
	exit 1
fi

cli=$(tr -d '[:space:]' <.codeql-version)
cli=${cli#v}
base=${semver%%-*}
if [ "$base" != "$cli" ]; then
	echo "release-version: $tag is for CodeQL $base, but .codeql-version pins $cli" >&2
	exit 1
fi

if [ "$mode" = name ]; then
	echo "release-version: $tag is a release of CodeQL $cli"
	exit 0
fi
if [ "$mode" = set ]; then
	for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
		[ -e "$manifest" ] || continue
		sed -i.bak "s/^version:.*/version: $semver/" "$manifest"
		rm -f "$manifest.bak"
	done
fi

fail=0
for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
	[ -e "$manifest" ] || continue
	version=$(awk '/^version:/ {print $2; exit}' "$manifest")
	if [ "$version" != "$semver" ]; then
		echo "release-version: $manifest is $version, not $semver" >&2
		fail=1
	fi
done
./scripts/check-versions.sh || fail=1

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "release-version: ready to release $tag"
