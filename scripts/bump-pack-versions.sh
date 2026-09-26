#!/usr/bin/env bash
#
# Keeps the version numbers that have to agree, agreeing.
#
#   bump-pack-versions.sh <cli-version>   set every pack's version to the CLI version
#   bump-pack-versions.sh --upgrade-deps  move each upstream dependency to the latest compatible
#   bump-pack-versions.sh --repin         rewrite each suite's `version:` from the lock files
#
# The last two modes exist because `codeql pack upgrade` alone does neither. It never looks inside
# a .qls, so an upgrade that appears clean leaves the suites pinned to a version nothing else uses.
# And it is a no-op against an exact pin, because the pin already satisfies its own constraint:
# measured 2026-09-09 with `codeql/actions-all: 0.6.0` and 0.6.1 published, it left both the
# manifest and the lock file at 0.6.0. Exact pinning is this repository's rule, so resolution has
# to be forced by wildcarding the dependency and reading the answer back out of the lock file.
set -euo pipefail

cd "$(dirname "$0")/.."

# Rewrites `key: value` in place for a top-level scalar, leaving comments and order alone.
set_scalar() {
	local file=$1 key=$2 value=$3
	awk -v k="$key" -v v="$value" '
		$0 ~ "^" k ":" && !done { print k ": " v; done = 1; next }
		{ print }
	' "$file" >"$file.tmp"
	mv "$file.tmp" "$file"
}

if [ "${1:-}" = "--upgrade-deps" ]; then
	# Libraries first: a pack depending on one through `${workspace}` resolves against the
	# library's pins, so upgrading it later leaves every dependent held at the old version.
	for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
		[ -e "$manifest" ] || continue
		pack_dir=$(dirname "$manifest")
		lock="$pack_dir/codeql-pack.lock.yml"

		# Only exact pins are forced. `${workspace}` resolves to a sibling in this repository and
		# a wildcard is already floating; wildcarding either would send the CLI to the registry
		# for a pack that is not published.
		pinned=$(awk '/^dependencies:/ {deps=1; next}
		              /^[^[:space:]]/ {deps=0}
		              deps && $1 ~ /:$/ && $2 ~ /^[0-9]+\./ {print substr($1, 1, length($1)-1)}
		             ' "$manifest")

		if [ -z "$pinned" ]; then
			# Still upgrade, so a pack whose sibling moved gets a refreshed lock file.
			codeql pack upgrade -- "$pack_dir" >/dev/null
			echo "$manifest: no exact pins; lock file refreshed"
			continue
		fi

		cp "$manifest" "$manifest.bak"
		for dep in $pinned; do
			set_scalar "$manifest" "  $dep" '"*"'
		done

		if ! codeql pack upgrade -- "$pack_dir" >/dev/null; then
			mv "$manifest.bak" "$manifest"
			echo "$manifest: codeql pack upgrade failed; pins left alone" >&2
			exit 1
		fi

		# Restore first, so a dependency the lock file does not name keeps the pin it had.
		mv "$manifest.bak" "$manifest"
		for dep in $pinned; do
			resolved=$(awk -v p="$dep:" '$1 == p {getline; print $2; exit}' "$lock")
			if [ -z "$resolved" ]; then
				echo "$manifest: $dep is not in $lock; pin left alone" >&2
				continue
			fi
			was=$(awk -v p="$dep:" '$1 == p {print $2; exit}' "$manifest")
			set_scalar "$manifest" "  $dep" "$resolved"
			if [ "$was" = "$resolved" ]; then
				echo "$manifest: $dep $resolved (already current)"
			else
				echo "$manifest: $dep $was -> $resolved"
			fi
		done
	done
	exit 0
fi

if [ "${1:-}" = "--repin" ]; then
	while IFS= read -r suite; do
		pack_dir=$suite
		while [ "$pack_dir" != "." ] && [ ! -f "$pack_dir/codeql-pack.yml" ]; do
			pack_dir=$(dirname "$pack_dir")
		done
		lock="$pack_dir/codeql-pack.lock.yml"
		[ -f "$lock" ] || continue

		# `from:` names the pack; the `version:` on the next line is what needs re-pinning.
		awk -v lock="$lock" '
			/^[[:space:]]*from:/ { from = $2 }
			/^[[:space:]]*version:/ && from != "" {
				cmd = "awk -v p=\"" from ":\" \x27$1 == p {getline; print $2; exit}\x27 " lock
				cmd | getline locked
				close(cmd)
				if (locked != "") { sub(/version:.*/, "version: " locked) }
				from = ""
			}
			{ print }
		' "$suite" >"$suite.tmp"
		mv "$suite.tmp" "$suite"

		# The manifest dependency has to say the same thing the suite now says.
		while read -r dep version; do
			set_scalar "$pack_dir/codeql-pack.yml" "  $dep" "$version"
		done < <(awk '/^[[:space:]]*from:/ {f=$2; next}
		              /^[[:space:]]*version:/ && f != "" {print f, $2; f=""}' "$suite")
	done < <(find ql -name '*.qls')
	./scripts/check-versions.sh
	exit 0
fi

cli=${1:?usage: bump-pack-versions.sh <cli-version> | --repin}
cli=${cli#v}

case "$cli" in
--*)
	echo "usage: bump-pack-versions.sh <cli-version> | --upgrade-deps | --repin" >&2
	exit 1
	;;
esac

for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
	[ -e "$manifest" ] || continue
	set_scalar "$manifest" version "$cli"
	echo "bumped $manifest to $cli"
done
