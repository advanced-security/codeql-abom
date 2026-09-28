#!/usr/bin/env bash
#
# Asserts that every version naming the same thing says the same thing.
#
# Exact pins appear in suites, manifests and lock files; all three must agree.
set -uo pipefail

cd "$(dirname "$0")/.."

fail=0
note() {
	echo "check-versions: $1"
	fail=1
}

cli=$(tr -d '[:space:]' <.codeql-version)
cli=${cli#v}

# Packs track the CLI version; pre-release `*-next.*` builds are allowed.
for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
	[ -e "$manifest" ] || continue
	version=$(awk '/^version:/ {print $2; exit}' "$manifest")
	case "$version" in
	"$cli" | *-next.*) ;;
	*) note "$manifest is $version, but .codeql-version is $cli" ;;
	esac
done

# Suite pins must match the owning pack and its lock file.
while IFS= read -r suite; do
	pack_dir=$suite
	while [ "$pack_dir" != "." ] && [ ! -f "$pack_dir/codeql-pack.yml" ]; do
		pack_dir=$(dirname "$pack_dir")
	done
	if [ ! -f "$pack_dir/codeql-pack.yml" ]; then
		note "$suite is not inside a pack"
		continue
	fi

	# Process substitution keeps `fail` in this shell.
	while read -r from version; do
		declared=$(awk -v p="$from:" '$1 == p {print $2; exit}' "$pack_dir/codeql-pack.yml")
		if [ -z "$declared" ]; then
			note "$suite selects from $from, which $pack_dir/codeql-pack.yml does not depend on"
		elif [ "$declared" != "$version" ]; then
			note "$suite pins $from $version, but $pack_dir/codeql-pack.yml declares $declared"
		fi
		locked=$(awk -v p="$from:" '$1 == p {getline; print $2; exit}' \
			"$pack_dir/codeql-pack.lock.yml" 2>/dev/null)
		if [ -n "$locked" ] && [ "$locked" != "$version" ]; then
			note "$suite pins $from $version, but the lock file resolved $locked"
		fi
	done < <(awk '/^[[:space:]]*from:/ {from=$2; next}
	              /^[[:space:]]*version:/ && from != "" {print from, $2; from=""}' "$suite")
done < <(find ql -name '*.qls')

# ql-mcp tool packs move with the CodeQL CLI.
tools=$(grep -o 'toolsPackName + "@[0-9][0-9.]*"' internal/runner/printast.go | grep -o '[0-9][0-9.]*')
if [ "$tools" != "$cli" ]; then
	note "internal/runner/printast.go pins ql-mcp's tool pack at ${tools:-nothing}, but .codeql-version is $cli"
fi
qlmcp=$(grep -o 'codeql-development-mcp-server@[0-9][0-9.]*' internal/harness/copilot.go | grep -o '[0-9][0-9.]*$')
if [ "$qlmcp" != "$cli" ]; then
	note "internal/harness/copilot.go starts ql-mcp ${qlmcp:-nothing}, but .codeql-version is $cli"
fi

# The operator's pack is scaffolded against ABOM's actions library.
lib=$(awk '$1 == "codeql/actions-all:" {print $2; exit}' ql/actions/lib/codeql-pack.yml)
scaffold=$(grep -o 'ActionsAll = "[0-9][0-9.]*"' internal/customql/customql.go | grep -o '[0-9][0-9.]*')
if [ "$scaffold" != "$lib" ]; then
	note "internal/customql/customql.go scaffolds codeql/actions-all ${scaffold:-nothing}, but ql/actions/lib pins $lib"
fi

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "versions: consistent (CodeQL $cli)"
