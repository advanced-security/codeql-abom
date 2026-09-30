#!/usr/bin/env bash
#
# Builds and packs the release archive for the platform this runs on.
#
#   assemble.sh <version> <interface-dir> <out-dir>
#
# <interface-dir> is what `make release-ui` writes: the published wwwroot, the notices for the
# packages it was built from and the SDK that built it. Native only: SQLite is linked through CGO,
# so each platform is built on its own runner.
set -euo pipefail

cd "$(dirname "$0")/../.."

usage="usage: assemble.sh <version> <interface-dir> <out-dir>"
version=${1:?$usage}
ui=${2:?$usage}
out=${3:?$usage}
version=${version#v}

./scripts/release-version.sh --check "v$version" >/dev/null

os=$(go env GOOS)
arch=$(go env GOARCH)
name="codeql-abom_${version}_${os}_${arch}"
root="$out/$name"
rm -rf "$root" "$out/$name.tar.gz"
mkdir -p "$root"

cli=$(tr -d '[:space:]' <.codeql-version)
cli=${cli#v}
if [ "$os" = darwin ]; then
	# Otherwise the binary requires the macOS it was built on. 12.0 is Go's own floor. The flags
	# reach cgo's objects too, which Go's build cache would otherwise reuse at the host's version.
	export MACOSX_DEPLOYMENT_TARGET=${MACOSX_DEPLOYMENT_TARGET:-12.0}
	export CGO_CFLAGS="${CGO_CFLAGS:--O2 -g} -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
	export CGO_LDFLAGS="${CGO_LDFLAGS:--O2 -g} -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
fi
CGO_ENABLED=1 go build -trimpath -buildvcs=true \
	-ldflags "-s -w -X main.version=$version -X main.codeqlVersion=$cli" \
	-o "$root/codeql-abom" ./cmd/codeql-abom

cp -R "$ui/wwwroot" "$root/ui"

# Tracked files only, so no test pack, test database, pack cache or local state can ride along.
git ls-files ql | grep -Ev '^ql/[^/]+/[^/]+/test/' | while IFS= read -r file; do
	mkdir -p "$root/$(dirname "$file")"
	cp "$file" "$root/$file"
done
cp codeql-workspace.yml LICENSE "$root/"

{
	echo "Third-party software in codeql-abom $version for $os/$arch."
	echo "The CodeQL CLI is not included; it is installed separately."
	echo
	python3 scripts/release/notices.py go "$root/codeql-abom"
	cat "$ui/ui-notices.txt"
} >"$root/THIRD_PARTY_NOTICES"

python3 scripts/release/build_info.py write "$root" --version "$version" \
	--ui-tools "$ui/ui-tools.json"

# macOS tar otherwise adds AppleDouble `._` entries that the manifest does not list.
COPYFILE_DISABLE=1 tar -C "$out" -czf "$out/$name.tar.gz" "$name"
echo "$out/$name.tar.gz"
