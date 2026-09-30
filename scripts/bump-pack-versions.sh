#!/usr/bin/env bash
#
# Keeps the version numbers that have to agree, agreeing.
#
#   bump-pack-versions.sh <version>       set every pack's version to the pinned CLI version, or to
#                                         a pre-release of it such as 2.27.1-rc1
#   bump-pack-versions.sh --upgrade-deps  move each upstream dependency to the latest compatible
#   bump-pack-versions.sh --repin         rewrite each suite's `version:` from the lock files
#
# Only each pack's own `version:` takes a pre-release suffix. Upstream pins are someone else's
# releases and are only ever moved to what a lock file resolved.
#
# `codeql pack upgrade` does not update suites or exact pins; force resolution by wildcarding
# each exact dependency and reading the resolved version back from the lock file.
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
	# Libraries first: workspace dependents resolve through their pins.
	for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
		[ -e "$manifest" ] || continue
		pack_dir=$(dirname "$manifest")
		lock="$pack_dir/codeql-pack.lock.yml"

		# Only exact pins are forced; workspace and wildcard dependencies already float locally.
		pinned=$(awk '/^dependencies:/ {deps=1; next}
		              /^[^[:space:]]/ {deps=0}
		              deps && $1 ~ /:$/ && $2 ~ /^[0-9]+\./ {print substr($1, 1, length($1)-1)}
		             ' "$manifest")

		if [ -z "$pinned" ]; then
			# Refresh lock files even when no exact dependency needs forcing.
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

		# Restore first so unresolved dependencies keep their existing pins.
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

		# Keep the manifest dependency aligned with the suite pin.
		while read -r dep version; do
			set_scalar "$pack_dir/codeql-pack.yml" "  $dep" "$version"
		done < <(awk '/^[[:space:]]*from:/ {f=$2; next}
		              /^[[:space:]]*version:/ && f != "" {print f, $2; f=""}' "$suite")
	done < <(find ql -name '*.qls')
	./scripts/check-versions.sh
	exit 0
fi

cli=${1:?usage: bump-pack-versions.sh <version> | --upgrade-deps | --repin}
cli=${cli#v}

case "$cli" in
--*)
	echo "usage: bump-pack-versions.sh <version> | --upgrade-deps | --repin" >&2
	exit 1
	;;
esac

# Pin the CLI first: a pack version names the CLI it is compiled for.
pinned=$(tr -d '[:space:]' <.codeql-version)
pinned=${pinned#v}
case "$cli" in
"$pinned" | "$pinned"-*) ;;
*)
	echo "bump-pack-versions: $cli is not CodeQL $pinned from .codeql-version, or a pre-release of it" >&2
	exit 1
	;;
esac

for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
	[ -e "$manifest" ] || continue
	set_scalar "$manifest" version "$cli"
	echo "bumped $manifest to $cli"
done
