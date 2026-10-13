#!/usr/bin/env bash
# Prebuild: install the tools a Demo runs, at the versions this checkout names. No user credential
# exists here and none is asked for; nothing a user later does is written into the prebuild.
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=.devcontainer/lib.sh
. "$PWD/.devcontainer/lib.sh"

# ql-mcp releases alongside the CodeQL CLI, and ABOM names the release that matches it.
codeql=$(tr -d '[:space:]' <.codeql-version)
copilot=1.0.88

case "$(uname -m)" in
x86_64) archive="codeql-linux64.zip" ;;
aarch64 | arm64) archive="codeql-linux-arm64.zip" ;;
*)
	echo "on-create: CodeQL publishes no Linux build for $(uname -m)" >&2
	exit 1
	;;
esac
echo "on-create: installing CodeQL $codeql"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
release="https://github.com/github/codeql-cli-binaries/releases/download/$codeql"
curl -fsSL --retry 5 --retry-all-errors -o "$scratch/$archive" "$release/$archive"
curl -fsSL --retry 5 --retry-all-errors -o "$scratch/$archive.checksum.txt" "$release/$archive.checksum.txt"
(cd "$scratch" && sha256sum --check --strict --quiet "$archive.checksum.txt")
sudo rm -rf /opt/codeql
sudo unzip -q "$scratch/$archive" -d /opt
test "v$(/opt/codeql/codeql version --format=terse)" = "$codeql"

# Installed as the Demo's own user, which runs them: ql-mcp's packs land where it resolves them,
# and no session downloads a tool or a pack.
echo "on-create: installing the Copilot CLI and ql-mcp for the Demo's user"
sudo install -d -o "$(id -u)" -g abom-demo -m 0755 /opt/abom-demo
sudo install -d -o abom-demo -g abom-demo -m 0755 /opt/abom-demo/tools
as_demo() { sudo -u abom-demo -H env -i PATH="/opt/codeql:$PATH" HOME=/home/abom-demo LANG=C.UTF-8 "$@"; }
retry as_demo npm install --prefix /opt/abom-demo/tools --no-fund --no-audit --no-update-notifier \
	"@github/copilot@$copilot" "codeql-development-mcp-server@${codeql#v}"
echo "on-create: installing ql-mcp's Actions tool packs"
retry as_demo env CODEQL_PATH=/opt/codeql/codeql \
	/opt/abom-demo/tools/node_modules/.bin/codeql-development-mcp-server-setup-packs --language actions
