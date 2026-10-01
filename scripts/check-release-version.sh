#!/usr/bin/env bash
#
# Proves a pre-release suffix reaches ABOM's own pack versions and nothing else.
#
# Runs release-version.sh and then bump-pack-versions.sh --repin in a scratch copy, for release
# candidates, `-next.N` builds and the release itself. The CLI pin, the upstream pack pins in
# manifests, suites and lock files, and the ql-mcp releases ABOM names are all someone else's
# regular releases; a suffix on any of them names something that was never published. Then it
# plants each such leak and requires check-versions.sh to refuse it.
set -euo pipefail

cd "$(dirname "$0")/.."

scratch=$(mktemp -d "${TMPDIR:-/tmp}/abom-release-version.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
tree="$scratch/tree"

# The working tree, not HEAD, and whatever check-versions reads beyond scripts/ and ql/, found
# rather than listed so the copy keeps up with it.
{
	git ls-files --cached --others --exclude-standard -- scripts ql .codeql-version
	grep -o 'internal/[A-Za-z0-9_/]*\.go' scripts/check-versions.sh | sort -u
} | sort -u | while IFS= read -r file; do
	[ -f "$file" ] || continue
	mkdir -p "$tree/$(dirname "$file")"
	cp -p "$file" "$tree/$file"
done
cd "$tree"

fail=0
bad() {
	echo "check-release-version: $1" >&2
	fail=1
}

manifests() {
	for manifest in ql/*/lib/codeql-pack.yml ql/*/*/src/codeql-pack.yml ql/*/*/test/codeql-pack.yml; do
		[ -e "$manifest" ] && echo "$manifest"
	done
}

# Every file's content with ABOM's own pack versions blanked, so any other difference is a leak.
snapshot() {
	find . -type f | sort | while IFS= read -r file; do
		case "$file" in
		./ql/*/lib/codeql-pack.yml | ./ql/*/*/src/codeql-pack.yml | ./ql/*/*/test/codeql-pack.yml)
			sed 's/^version:.*/version: <abom>/' "$file"
			;;
		*) cat "$file" ;;
		esac | shasum -a 256 | sed "s|-\$|$file|"
	done
}

cli=$(tr -d '[:space:]' <.codeql-version)
cli=${cli#v}
baseline=$(snapshot)

for version in "v$cli-rc1" "v$cli-rc.2" "v$cli-next.1" "v$cli-next.2" "v$cli"; do
	semver=${version#v}
	if ! ./scripts/release-version.sh "$version" >/dev/null 2>"$scratch/.err"; then
		bad "release-version.sh $version failed: $(cat "$scratch/.err")"
		continue
	fi
	if ! ./scripts/bump-pack-versions.sh --repin >/dev/null 2>"$scratch/.err"; then
		bad "bump-pack-versions.sh --repin failed after $version: $(cat "$scratch/.err")"
	fi
	while IFS= read -r manifest; do
		got=$(awk '/^version:/ {print $2; exit}' "$manifest")
		[ "$got" = "$semver" ] || bad "$manifest is $got after releasing $version"
	done < <(manifests)
	moved=$(diff <(echo "$baseline") <(snapshot) | sed -n 's/^> [0-9a-f]* *//p' || true)
	if [ -n "$moved" ]; then
		bad "$version changed more than ABOM's pack versions: $(echo "$moved" | tr '\n' ' ')"
	fi
done

if ./scripts/release-version.sh "v9.9.9-rc1" >/dev/null 2>&1; then
	bad "a release of a CLI other than the pinned one was accepted"
fi
if ./scripts/bump-pack-versions.sh "9.9.9-rc1" >/dev/null 2>&1; then
	bad "bump-pack-versions.sh accepted a version of a CLI other than the pinned one"
fi
while IFS= read -r manifest; do
	got=$(awk '/^version:/ {print $2; exit}' "$manifest")
	[ "$got" = "$cli" ] || bad "a refused version left $manifest at $got"
done < <(manifests)
./scripts/release-version.sh "v$cli-rc1" >/dev/null
if ./scripts/release-version.sh --check "v$cli" >/dev/null 2>&1; then
	bad "--check v$cli accepted packs still at $cli-rc1"
fi
./scripts/release-version.sh "v$cli" >/dev/null

# Each leak is planted in an otherwise consistent tree, and check-versions has to see it.
planted() {
	local what=$1 file=$2
	shift 2
	cp "$file" "$scratch/.saved"
	"$@" <"$scratch/.saved" >"$file"
	if cmp -s "$file" "$scratch/.saved"; then
		bad "planting $what changed nothing in $file"
	elif ./scripts/check-versions.sh >/dev/null 2>&1; then
		bad "check-versions accepted $what"
	fi
	cp "$scratch/.saved" "$file"
}

first=$(manifests | head -1)
planted "packs at two different versions" "$first" sed "s/^version:.*/version: $cli-rc1/"
planted "a pre-release pinned as the CLI" .codeql-version sed "s/\$/-rc1/"
planted "a pre-release of ql-mcp's tool pack" internal/runner/printast.go \
	sed "s/\(toolsPackName + \"@[0-9.]*\)\"/\1-next.1\"/"
planted "a pre-release of the ql-mcp server" internal/harness/copilot.go \
	sed "s/\(codeql-development-mcp-server@[0-9.]*\)/\1-next.1/"
while IFS= read -r manifest; do
	if grep -qE '^  codeql/[a-z-]+: [0-9]' "$manifest"; then
		planted "a pre-release upstream pin in $manifest that its lock file never resolved" \
			"$manifest" awk '!done && /^  codeql\/[a-z-]+: [0-9.]+$/ { $0 = $0 "-rc1"; done = 1 } { print }'
	fi
done < <(manifests)

# Consistent everywhere is still wrong: the CLI pin names a release of the CodeQL CLI.
cp .codeql-version "$scratch/.saved"
echo "v$cli-rc1" >.codeql-version
./scripts/bump-pack-versions.sh "$cli-rc1" >/dev/null 2>&1 || true
sed -i.bak "s/\(toolsPackName + \"@\)[0-9.]*\"/\1$cli-rc1\"/" internal/runner/printast.go
sed -i.bak "s/\(codeql-development-mcp-server@\)[0-9.]*/\1$cli-rc1/" internal/harness/copilot.go
rm -f internal/runner/printast.go.bak internal/harness/copilot.go.bak
if ./scripts/check-versions.sh >/dev/null 2>&1; then
	bad "check-versions accepted a pre-release CLI pin that everything else agreed with"
fi

if [ "$fail" -ne 0 ]; then
	exit 1
fi
echo "release-version: a pre-release reaches ABOM's pack versions and nothing else"
