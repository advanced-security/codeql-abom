#!/usr/bin/env bash
#
# Keeps the version numbers that have to agree, agreeing.
#
#   bump-pack-versions.sh <cli-version>   set every pack's version to the CLI version
#   bump-pack-versions.sh --repin         rewrite each suite's `version:` from the lock files
#
# The second mode exists because `codeql pack upgrade` rewrites dependencies and lock files and
# never looks inside a .qls, so an upgrade that appears clean leaves the suites pinned to a
# version nothing else uses.
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

for manifest in ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
	[ -e "$manifest" ] || continue
	set_scalar "$manifest" version "$cli"
	echo "bumped $manifest to $cli"
done
