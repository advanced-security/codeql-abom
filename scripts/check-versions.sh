#!/usr/bin/env bash
#
# Asserts that every version naming the same thing says the same thing.
#
# Exact pinning is the rule here, which means an upstream version is written in three places:
# a suite's `from: ... version:`, the owning pack's `dependencies:`, and codeql-pack.lock.yml.
# `codeql pack upgrade` rewrites the last two and does NOT touch a version inside a .qls, so an
# upgrade that looks clean can leave the suites pinned to a version nothing else uses.
set -uo pipefail

cd "$(dirname "$0")/.."

fail=0
note() {
	echo "check-versions: $1"
	fail=1
}

cli=$(tr -d '[:space:]' <.codeql-version)
cli=${cli#v}

# A pack tracks the CLI it is compiled for. A pre-release attaches to the upcoming version, so
# `2.26.5-next.1` is fine while the CLI is 2.26.4; anything else is drift.
for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
	[ -e "$manifest" ] || continue
	version=$(awk '/^version:/ {print $2; exit}' "$manifest")
	case "$version" in
	"$cli" | *-next.*) ;;
	*) note "$manifest is $version, but .codeql-version is $cli" ;;
	esac
done

# Every `from:`/`version:` pair in a suite must match the dependency pin of the pack that owns
# the suite, and the lock file that pack resolved.
while IFS= read -r suite; do
	pack_dir=$suite
	while [ "$pack_dir" != "." ] && [ ! -f "$pack_dir/codeql-pack.yml" ]; do
		pack_dir=$(dirname "$pack_dir")
	done
	if [ ! -f "$pack_dir/codeql-pack.yml" ]; then
		note "$suite is not inside a pack"
		continue
	fi

	# `from:` names the pack and the `version:` on the following line pins it. Read through
	# process substitution, not a pipe: a pipe runs the loop in a subshell and `fail` never
	# escapes it, so every mismatch would be reported and then exit 0.
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

# ql-mcp is released with each CodeQL, and its tool packs depend on that release's libraries. A
# session and ABOM's own PrintAST read fixtures through them, so they move with the CLI; a pin left
# behind is a query written against one library and run against another.
tools=$(grep -o 'toolsPackName + "@[0-9][0-9.]*"' internal/runner/printast.go | grep -o '[0-9][0-9.]*')
if [ "$tools" != "$cli" ]; then
	note "internal/runner/printast.go pins ql-mcp's tool pack at ${tools:-nothing}, but .codeql-version is $cli"
fi
qlmcp=$(grep -o 'codeql-development-mcp-server@[0-9][0-9.]*' internal/harness/copilot.go | grep -o '[0-9][0-9.]*$')
if [ "$qlmcp" != "$cli" ]; then
	note "internal/harness/copilot.go starts ql-mcp ${qlmcp:-nothing}, but .codeql-version is $cli"
fi

# The operator's pack is scaffolded against the actions library ABOM's own queries run.
lib=$(awk '$1 == "codeql/actions-all:" {print $2; exit}' ql/actions/lib/codeql-pack.yml)
scaffold=$(grep -o 'ActionsAll = "[0-9][0-9.]*"' internal/customql/customql.go | grep -o '[0-9][0-9.]*')
if [ "$scaffold" != "$lib" ]; then
	note "internal/customql/customql.go scaffolds codeql/actions-all ${scaffold:-nothing}, but ql/actions/lib pins $lib"
fi

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "versions: consistent (CodeQL $cli)"
