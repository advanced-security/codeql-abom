#!/usr/bin/env bash
# Each time an editor attaches: say where the Demo is, and what Copilot authoring still needs.
set -uo pipefail
port=8099
instance=$(curl -fsS --max-time 5 "http://127.0.0.1:$port/api/instance" 2>/dev/null) || {
	echo "ABOM Demo: not answering yet. It starts with the Codespace; see /tmp/abom-demo-logs/serve.log,"
	echo "or start it again with .devcontainer/start-demo.sh"
	exit 0
}
echo "ABOM Demo: open port $port (\"ABOM Demo\") from the Ports panel."
# The Demo admits loopback and the one forwarded address it was started with, so a Demo started
# where nothing named that address refuses every request a browser makes.
if [ -n "${CODESPACE_NAME:-}" ] && [ -n "${GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN:-}" ]; then
	forwarded="$CODESPACE_NAME-$port.$GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN"
	code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -H "Host: $forwarded" \
		"http://127.0.0.1:$port/api/instance")
	if [ "$code" = 403 ]; then
		echo "ABOM Demo: it refuses $forwarded, the address a browser uses. Restart it from a terminal:"
		echo "    .devcontainer/start-demo.sh"
	fi
fi
case "$instance" in
*'"authoring":"copilot"'*)
	readiness=$(curl -fsS --max-time 15 "http://127.0.0.1:$port/api/authoring/readiness" 2>/dev/null)
	case "$readiness" in
	*'"ready":true'*) echo "Copilot authoring: ready." ;;
	*)
		echo "Copilot authoring needs your GitHub sign-in. In a terminal, run:"
		echo "    .devcontainer/copilot-login.sh"
		;;
	esac
	;;
*) echo "Copilot authoring: unavailable here; /tmp/abom-demo-logs/serve.log says why." ;;
esac
