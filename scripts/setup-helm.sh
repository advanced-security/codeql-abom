#!/usr/bin/env bash
#
# Installs the pinned Helm, after checking its archive against the SHA-256 recorded here.
#
#   setup-helm.sh [dir]   default: $HOME/.local/bin
#
# `make chart` lints and renders the chart with it, so a pull request, a release and an agent all
# render with the same Helm. A runner's preinstalled Helm is whatever its image shipped.
set -euo pipefail

version=v4.3.1
dir=${1:-$HOME/.local/bin}

case "$(uname -s)-$(uname -m)" in
Linux-x86_64) platform=linux-amd64 sum=0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b ;;
Linux-aarch64 | Linux-arm64) platform=linux-arm64 sum=8cf8f66ecbc252ac8f7f06cca68aba88508ddfde3a6a34feb1678ad1694b4f88 ;;
Darwin-arm64) platform=darwin-arm64 sum=6b4e61c8cd27d99e5a801e63ff25dd5c70b36875c2cd659d3e9ce6b76fc23198 ;;
Darwin-x86_64) platform=darwin-amd64 sum=34bec4eba761ba5c16d1965ee6b8f5b0abe5993974a802274e2a4f0418893d89 ;;
*)
	echo "setup-helm: no recorded Helm $version for $(uname -s)-$(uname -m)" >&2
	exit 2
	;;
esac

if [ -x "$dir/helm" ] && "$dir/helm" version --short 2>/dev/null | grep -q "^$version+"; then
	echo "setup-helm: Helm $version is already in $dir"
else
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	archive="helm-$version-$platform.tar.gz"
	curl -fsSL --retry 3 -o "$tmp/$archive" "https://get.helm.sh/$archive"
	got=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
	if [ "$got" != "$sum" ]; then
		echo "setup-helm: $archive has SHA-256 $got, not the recorded $sum" >&2
		exit 1
	fi
	tar -xzf "$tmp/$archive" -C "$tmp"
	mkdir -p "$dir"
	install -m 0755 "$tmp/$platform/helm" "$dir/helm"
	echo "setup-helm: installed Helm $version into $dir"
fi

if [ -n "${GITHUB_PATH:-}" ]; then
	echo "$dir" >>"$GITHUB_PATH"
fi
