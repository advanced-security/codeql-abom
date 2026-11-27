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

# The pin names a CodeQL CLI release, and those never carry a pre-release suffix.
if ! [[ "$cli" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	note ".codeql-version is v$cli, which is not a CodeQL CLI release"
fi

# Packs track the CLI version; a pre-release of that same version, such as `2.27.1-next.1`, is a
# further ABOM release against the same CLI. Every pack is part of one release.
abom=""
for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
	[ -e "$manifest" ] || continue
	version=$(awk '/^version:/ {print $2; exit}' "$manifest")
	case "$version" in
	"$cli" | "$cli"-*) ;;
	*) note "$manifest is $version, but .codeql-version is $cli" ;;
	esac
	if [ -z "$abom" ]; then
		abom=$version
		first=$manifest
	elif [ "$version" != "$abom" ]; then
		note "$manifest is $version, but $first is $abom"
	fi

	# An exact upstream pin is what the lock file resolved, never a version upstream did not publish.
	lock="$(dirname "$manifest")/codeql-pack.lock.yml"
	while read -r dep pin; do
		locked=$(awk -v p="$dep:" '$1 == p {getline; print $2; exit}' "$lock" 2>/dev/null)
		if [ "$locked" != "$pin" ]; then
			note "$manifest pins $dep $pin, but $lock resolved ${locked:-nothing}"
		fi
	done < <(awk '/^dependencies:/ {deps=1; next}
	              /^[^[:space:]]/ {deps=0}
	              deps && $1 ~ /:$/ && $2 ~ /^[0-9]/ {print substr($1, 1, length($1)-1), $2}' "$manifest")
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

# ql-mcp releases alongside the CodeQL CLI, and ABOM names that release, never a pre-release of
# it. Read whole, so a suffix is compared rather than cut off.
tools=$(sed -n 's/.*toolsPackName + "@\([^"]*\)".*/\1/p' internal/runner/printast.go | sed -n 1p)
if [ "$tools" != "$cli" ]; then
	note "internal/runner/printast.go pins ql-mcp's tool pack at ${tools:-nothing}, but .codeql-version is $cli"
fi
qlmcp=$(sed -n 's/.*codeql-development-mcp-server@\([^" ]*\).*/\1/p' internal/harness/copilot.go | sed -n 1p)
if [ "$qlmcp" != "$cli" ]; then
	note "internal/harness/copilot.go starts ql-mcp ${qlmcp:-nothing}, but .codeql-version is $cli"
fi

# The operator's pack is scaffolded against ABOM's actions library.
lib=$(awk '$1 == "codeql/actions-all:" {print $2; exit}' ql/actions/lib/codeql-pack.yml)
scaffold=$(sed -n 's/.*ActionsAll = "\([^"]*\)".*/\1/p' internal/customql/customql.go | sed -n 1p)
if [ "$scaffold" != "$lib" ]; then
	note "internal/customql/customql.go scaffolds codeql/actions-all ${scaffold:-nothing}, but ql/actions/lib pins $lib"
fi

# The image builds with the Go the module names, pinned by digest like every base it starts from.
toolchain=$(awk '$1 == "toolchain" {sub(/^go/, "", $2); print $2; exit}' go.mod 2>/dev/null || true)
image_go=$(sed -n 's/^FROM .*golang:\([0-9.]*\)-.*/\1/p' infra/docker/Dockerfile 2>/dev/null | sed -n 1p)
if [ -z "$toolchain" ]; then
	note "go.mod names no toolchain to build the image with"
elif [ "$image_go" != "$toolchain" ]; then
	note "infra/docker/Dockerfile builds with Go ${image_go:-unpinned}, but go.mod's toolchain is go$toolchain"
fi
while read -r base; do
	case "$base" in
	*@sha256:*) ;;
	*) note "infra/docker/Dockerfile starts from $base, which is not pinned by digest" ;;
	esac
done < <(awk '$1 == "FROM" {for (i = 2; i <= NF; i++) if ($i !~ /^--/) {print $i; break}}' infra/docker/Dockerfile 2>/dev/null)

# The query repository's workflows are copied out and locked there, so nothing here verifies them;
# each action they use is the one this repository's lock holds, so a copy starts from audited tags.
templates=(infra/qlaas-repo-template/workflows/*.yml)
[ -f "${templates[0]}" ] || note "infra/qlaas-repo-template/workflows holds no workflow to check"
while read -r file ref; do
	grep -qF "'$ref'" .github/workflows/actions.lock || note "$file uses $ref, which .github/workflows/actions.lock does not hold"
done < <(awk '$1 == "-" && $2 == "uses:" {print FILENAME, $3} $1 == "uses:" {print FILENAME, $2}' \
	"${templates[@]}" 2>/dev/null | grep -v ' \./')

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "versions: consistent (CodeQL $cli, Go $toolchain)"
