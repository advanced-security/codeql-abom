#!/usr/bin/env bash
# Prebuild and content update: build ABOM and its interface from this checkout, and warm the CodeQL
# packages the Demo's user resolves, so no run or session downloads a pack.
set -euo pipefail
cd "$(dirname "$0")/../.."

make build
install -m 0755 bin/codeql-abom /opt/abom-demo/codeql-abom
make publish ABOM_UI=/opt/abom-demo/ui

scratch=$(sudo -u abom-demo mktemp -d)
trap 'sudo rm -rf "$scratch"' EXIT
as_demo() { sudo -u abom-demo -H env -i PATH="$PATH" HOME=/home/abom-demo LANG=C.UTF-8 "$@"; }
as_demo /opt/abom-demo/codeql-abom demo init --dir "$scratch/demo" --sources "$PWD/testdata/corpus"
as_demo /opt/codeql/codeql pack install "$scratch/demo/ql"
as_demo /opt/codeql/codeql pack install --additional-packs "$scratch/demo/ql" "$scratch/demo/ql/test"

# Installing reaches a registry even with every dependency cached, so each new Demo takes these
# locks and its runner installs nothing.
mkdir -p /opt/abom-demo/pack-locks/test
sudo cat "$scratch/demo/ql/codeql-pack.lock.yml" >/opt/abom-demo/pack-locks/codeql-pack.lock.yml
sudo cat "$scratch/demo/ql/test/codeql-pack.lock.yml" >/opt/abom-demo/pack-locks/test/codeql-pack.lock.yml
