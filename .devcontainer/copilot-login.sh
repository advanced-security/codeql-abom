#!/usr/bin/env bash
# Give the Demo's Copilot authoring sessions your own GitHub sign-in.
#
#   copilot-login.sh                 sign in with gh, through a one-time code in your browser, unless
#                                    already signed in, and give that sign-in to the Demo
#   copilot-login.sh --token         read a fine-grained token with the Copilot Requests permission
#   copilot-login.sh --if-signed-in  give the Demo an existing sign-in, never asking (each start runs this)
#
# The Codespace's own GITHUB_TOKEN is never used: it belongs to this repository, not to you. The
# credential is kept in the Demo's private directory, readable only by the Demo's user, and handed to
# no process but the Copilot CLI.
set -euo pipefail
root=${ABOM_DEMO_ROOT:-/tmp/abom-demo}

die() {
	printf 'copilot-login: %s\n' "$*" >&2
	exit 1
}

# gh without any exported token, so it answers with the sign-in `gh auth login` stored.
user_gh() { env -u GITHUB_TOKEN -u GH_TOKEN -u GITHUB_ENTERPRISE_TOKEN -u GH_ENTERPRISE_TOKEN "$@"; }

[ -f "$root/demo.json" ] || die "No Demo is running in this Codespace; start one with .devcontainer/start-demo.sh"

case "${1:-}" in
--token)
	if [ -t 0 ]; then
		echo "Paste a fine-grained personal access token with the Copilot Requests permission."
		read -rsp "Token: " token
		echo
	else
		read -r token || true
	fi
	;;
--if-signed-in)
	token=$(user_gh gh auth token --hostname github.com 2>/dev/null) || exit 0
	;;
"")
	if ! token=$(user_gh gh auth token --hostname github.com 2>/dev/null); then
		echo "Sign in to GitHub: open the address below and enter the one-time code it shows."
		user_gh GH_PROMPT_DISABLED=1 gh auth login --hostname github.com --web --git-protocol https --skip-ssh-key
		token=$(user_gh gh auth token --hostname github.com)
	fi
	;;
*) die "usage: copilot-login.sh [--token | --if-signed-in]" ;;
esac

token=$(printf '%s' "${token:-}" | tr -d '[:space:]')
case "$token" in
"") die "no credential was entered, so none was saved" ;;
ghp_*) die "Copilot does not accept classic personal access tokens (ghp_); sign in with gh, or use a fine-grained token" ;;
esac
printf '%s\n' "$token" | sudo -u abom-demo sh -c 'umask 077; cat >"$1"' sh "$root/secrets/copilot-token"
echo "copilot-login: saved. The next authoring session uses it, and each start of this Codespace gives it to the new Demo."
