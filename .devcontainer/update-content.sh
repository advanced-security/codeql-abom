#!/usr/bin/env bash
# Prebuild and content update: build ABOM and its interface from this checkout, and put every pack a
# Demo resolves in its user's package cache, so no run, test or session downloads one.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=.devcontainer/lib.sh
. "$PWD/.devcontainer/lib.sh"
codeql=$(tr -d '[:space:]' <.codeql-version)

# Downloading first leaves the builds nothing to fetch, so only the downloads need retrying.
echo "update-content: building ABOM"
retry go mod download
make build
install -m 0755 bin/codeql-abom /opt/abom-demo/codeql-abom
echo "update-content: publishing its interface"
retry dotnet restore ui/Abom.WebAssembly.csproj
make publish ABOM_UI=/opt/abom-demo/ui

scratch=$(sudo -u abom-demo mktemp -d)
trap 'sudo rm -rf "$scratch"' EXIT
as_demo() { sudo -u abom-demo -H env -i PATH="/opt/codeql:$PATH" HOME=/home/abom-demo LANG=C.UTF-8 "$@"; }
# Every run resolves ABOM's own findings suite and the upstream packs it imports. Each is installed
# from its lock, because the Demo's user cannot write this checkout.
echo "update-content: installing ABOM's query packs for the Demo's user"
while IFS= read -r manifest; do
	retry as_demo codeql pack ci "$PWD/$(dirname "$manifest")" </dev/null
done < <(find ql/actions -name codeql-pack.yml | sort)
echo "update-content: installing the packs a Demo's own queries and tests use"
as_demo /opt/abom-demo/codeql-abom demo init --dir "$scratch/demo" --sources "$PWD/testdata/corpus"
retry as_demo codeql pack install "$scratch/demo/ql"
retry as_demo codeql pack install --additional-packs "$scratch/demo/ql" "$scratch/demo/ql/test"
# ABOM's own PrintAST runs the Actions tool pack ql-mcp publishes for this CodeQL release.
retry as_demo codeql pack download "advanced-security/ql-mcp-actions-tools-src@${codeql#v}"

# Installing reaches a registry even with every dependency cached, so each new Demo takes these
# locks and its runner installs nothing.
mkdir -p /opt/abom-demo/pack-locks/test
for lock in codeql-pack.lock.yml test/codeql-pack.lock.yml; do
	sudo cat "$scratch/demo/ql/$lock" | tee "/opt/abom-demo/pack-locks/$lock" >/dev/null
done
