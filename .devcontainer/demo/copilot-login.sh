#!/usr/bin/env bash
# Supply the Copilot credential the Demo's authoring sessions use. Read without echo, kept only in
# this Codespace's private Demo directory, and handed to no process but the Copilot CLI.
set -euo pipefail
root=/tmp/abom-demo
if [ ! -f "$root/demo.json" ]; then
	echo "No Demo is running in this Codespace; start one with .devcontainer/demo/start-demo.sh" >&2
	exit 1
fi
echo "Paste a fine-grained personal access token that has the Copilot Requests permission."
echo "It stays in this Codespace until the Codespace stops; revoke it on GitHub when you are done."
read -rsp "Token: " token
echo
if [ -z "$token" ]; then
	echo "Nothing was entered; no credential was saved." >&2
	exit 1
fi
printf '%s\n' "$token" | sudo -u abom-demo sh -c 'umask 077; cat >"$1"' sh "$root/secrets/copilot-token"
echo "Saved. The next authoring session uses it."
