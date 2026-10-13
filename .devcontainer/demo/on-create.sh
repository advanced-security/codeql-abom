#!/usr/bin/env bash
# Prebuild: install the pinned tools a Demo runs. No user credential exists here and none is asked
# for; nothing a user later does is written into the prebuild.
set -euo pipefail
cd "$(dirname "$0")/../.."

want=$(tr -d '[:space:]' <.codeql-version)
want=${want#v}
have=$(/opt/codeql/codeql version --format=terse)
if [ "$want" != "$have" ]; then
	echo "on-create: the image holds CodeQL $have and .codeql-version pins $want;" \
		"update CODEQL_VERSION in .devcontainer/demo/Dockerfile" >&2
	exit 1
fi

# Installed once here, so no session downloads a different one while it runs.
npm install --global --no-fund --no-audit @github/copilot@1.0.88 codeql-development-mcp-server@2.27.1
sudo install -d -o "$(id -u)" -g abom-demo -m 0755 /opt/abom-demo
