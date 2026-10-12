#!/usr/bin/env bash
# Set up and run an ABOM Demo from a checkout or an archive of this repository.
#
#   scripts/demo.sh check   report each prerequisite, and what to install where one is missing
#   scripts/demo.sh setup   install query packs and create the Demo workspace, unless it exists
#   scripts/demo.sh serve   serve the Demo in the foreground; Ctrl-C stops it
#   scripts/demo.sh login   save the Copilot credential sessions use, read without echo
#   scripts/demo.sh reset   start the workspace over with a new dataset
#
# `make demo`, `make demo-check`, `make demo-login` and `make demo-reset` call these.
set -euo pipefail
cd "$(dirname "$0")/.."

dir=${ABOM_DEMO_DIR:-$PWD/.abom/demo}
addr=${ABOM_DEMO_ADDR:-127.0.0.1:8090}
egress=${ABOM_DEMO_EGRESS:-127.0.0.1:8091}
credits=${ABOM_DEMO_CREDITS:-0}
ui=${ABOM_UI:-$PWD/.abom/ui}/wwwroot
bin=$PWD/bin/codeql-abom
pin=$(tr -d '[:space:]' <.codeql-version)
pin=${pin#v}

# usable reports whether a CodeQL version is the pin, or a later patch release of its minor version.
usable() {
	local want_minor=${pin%.*} want_patch=${pin##*.}
	[ "${1%.*}" = "$want_minor" ] && [ "${1##*.}" -ge "$want_patch" ] 2>/dev/null
}

say() { printf 'demo: %s\n' "$*"; }
die() { printf 'demo: %s\n' "$*" >&2; exit 1; }

# quietly runs a command and shows its output only when it fails.
quietly() {
	local out
	if ! out=$("$@" 2>&1); then
		printf '%s\n' "$out" >&2
		die "failed: $*"
	fi
}

# codeql_cli prints the CodeQL executable itself, not a `gh codeql` shim, which a Demo cannot use
# because it gives gh no credential.
codeql_cli() {
	local json loc
	json=$(${CODEQL:-codeql} version --format=json 2>/dev/null | tr -d '\n') || return 1
	loc=$(printf '%s' "$json" | sed -n 's/.*"unpackedLocation" *: *"\([^"]*\)".*/\1/p')
	[ -n "$loc" ] && [ -x "$loc/codeql" ] || return 1
	printf '%s/codeql\n' "$loc"
}

# copilot_cli prints a Copilot CLI that answers --version, ignoring an editor's shim that waits.
copilot_cli() {
	local cli=${COPILOT_CLI_PATH:-$(command -v copilot || true)}
	[ -n "$cli" ] && "$cli" --version </dev/null 2>/dev/null | grep -q "Copilot CLI" || return 1
	printf '%s\n' "$cli"
}

check() {
	local missing=0 cli version
	have() {
		if command -v "$1" >/dev/null 2>&1; then
			printf '  ok       %-34s %s\n' "$1" "$(command -v "$1")"
		else
			printf '  MISSING  %-34s %s\n' "$1" "$2"
			[ "${3:-required}" = optional ] || missing=1
		fi
	}
	say "required to build and serve"
	have make "Xcode Command Line Tools on macOS; build-essential on Linux"
	have cc "Xcode Command Line Tools on macOS; build-essential on Linux"
	have git "git-scm.com; it builds the fictional repositories"
	have curl "your package manager"
	have go "go.dev/dl, or: brew install go"
	have dotnet ".NET 10 SDK: dotnet.microsoft.com/download, or: brew install --cask dotnet-sdk"
	have dot "Graphviz: brew install graphviz, or: apt-get install graphviz"
	if cli=$(codeql_cli); then
		version=$("$cli" version --format=terse)
		if usable "$version"; then
			printf '  ok       %-34s %s\n' "codeql $version" "$cli"
		else
			printf '  MISSING  %-34s %s\n' "codeql ${pin%.*}.x" "found $version at $cli; install it as docs/demo.md shows"
			missing=1
		fi
	else
		printf '  MISSING  %-34s %s\n' "codeql ${pin%.*}.x" "gh codeql set-version, as docs/demo.md shows"
		missing=1
	fi
	say "optional, for Copilot authoring"
	have node "Node.js 24.15 or later: nodejs.org, or: brew install node" optional
	if cli=$(copilot_cli); then
		printf '  ok       %-34s %s\n' "copilot" "$cli"
	else
		printf '  MISSING  %-34s %s\n' "copilot" "npm install --global @github/copilot@1.0.88"
	fi
	have codeql-development-mcp-server "npm install --global codeql-development-mcp-server@2.27.1" optional
	if [ -s "$dir/secrets/copilot-token" ]; then
		printf '  ok       %-34s %s\n' "Copilot credential" "saved for this Demo"
	else
		printf '  MISSING  %-34s %s\n' "Copilot credential" "make demo-login, once the Demo exists"
	fi
	[ "$missing" = 0 ] || die "install what is missing above, then run this again"
	say "everything required is installed"
}

# install_packs installs what the Demo's own query pack and its tests depend on, while the network
# is allowed, so the running Demo installs nothing.
install_packs() {
	local cli=$1
	quietly "$cli" pack install "$dir/ql"
	quietly "$cli" pack install --additional-packs "$dir/ql" "$dir/ql/test"
}

setup() {
	local cli manifest
	[ -x "$bin" ] || die "no $bin; run make demo, which builds it"
	if [ -f "$dir/demo.json" ]; then
		say "using the Demo at $dir"
		return
	fi
	cli=$(codeql_cli) || die "CodeQL ${pin%.*}.x is not installed; run make demo-check"
	say "installing ABOM's query packs (downloads from the CodeQL package registry)"
	while IFS= read -r manifest; do
		quietly "$cli" pack install "$(dirname "$manifest")"
	done < <(find ql -name codeql-pack.yml | sort)
	if command -v codeql-development-mcp-server-setup-packs >/dev/null; then
		say "installing ql-mcp's Actions tool packs, which sessions use and cannot download later"
		PATH="$(dirname "$cli"):$PATH" quietly codeql-development-mcp-server-setup-packs --language actions
	fi
	say "creating the Demo at $dir"
	"$bin" demo init --dir "$dir"
	install_packs "$cli"
}

answering() { curl -fsS -o /dev/null --max-time 2 "http://$addr/api/instance" 2>/dev/null; }

serve() {
	local cli copilot qlmcp args egress_pid="" token
	setup
	answering && die "something already answers on http://$addr; stop it, or set ABOM_DEMO_ADDR"
	cli=$(codeql_cli) || die "CodeQL ${pin%.*}.x is not installed; run make demo-check"
	args=(demo serve --dir "$dir" --addr "$addr" --ui-dir "$ui" --codeql "$cli")
	copilot=$(copilot_cli || true)
	qlmcp=$(command -v codeql-development-mcp-server || true)
	if [ -n "$copilot" ] && [ -n "$qlmcp" ]; then
		# Copilot is the one real service a Demo reaches, through a proxy that admits its hosts alone.
		rm -f "$dir/egress-ca.pem"
		"$bin" demo egress --addr "$egress" --ca-out "$dir/egress-ca.pem" >>"$dir/egress.log" 2>&1 &
		egress_pid=$!
		trap 'kill "$egress_pid" 2>/dev/null || true' EXIT
		for _ in 1 2 3 4 5 6 7 8 9 10; do
			[ -s "$dir/egress-ca.pem" ] && break
			sleep 0.5
		done
		[ -s "$dir/egress-ca.pem" ] || die "the egress proxy did not start; see $dir/egress.log"
		args+=(--copilot-cli "$copilot" --ql-mcp "$qlmcp" --egress-proxy "http://$egress"
			--egress-ca "$dir/egress-ca.pem" --authoring-credits "$credits")
		if [ ! -s "$dir/secrets/copilot-token" ] && token=$(gh auth token 2>/dev/null) && [ -n "$token" ]; then
			(umask 077 && printf '%s\n' "$token" >"$dir/secrets/copilot-token")
			say "Copilot uses your gh sign-in; make demo-login replaces it"
		fi
		[ -s "$dir/secrets/copilot-token" ] ||
			say "Copilot authoring needs a credential: run gh auth login, or make demo-login"
	else
		say "Copilot authoring is off: install the Copilot CLI and ql-mcp (make demo-check says how)"
	fi
	say "open http://$addr — Ctrl-C stops the Demo"
	"$bin" "${args[@]}"
}

login() {
	local token
	[ -d "$dir/secrets" ] || die "no Demo at $dir yet; run make demo first"
	if [ -t 0 ]; then
		echo "Paste a fine-grained personal access token with the Copilot Requests permission,"
		echo "or press Ctrl-C and run: gh auth token | make demo-login"
		read -rsp "Token: " token
		echo
	else
		read -r token || true
	fi
	token=$(printf '%s' "$token" | tr -d '[:space:]')
	case "$token" in
	"") die "nothing was entered; no credential was saved" ;;
	ghp_*) die "Copilot does not accept classic personal access tokens (ghp_); use a fine-grained one" ;;
	esac
	(umask 077 && printf '%s\n' "$token" >"$dir/secrets/copilot-token")
	say "saved in $dir/secrets; the next authoring session uses it, and only Copilot receives it"
}

reset() {
	local cli
	[ -x "$bin" ] || die "no $bin; run make demo-reset, which builds it"
	answering && die "the Demo is running on http://$addr; stop it first"
	if [ ! -f "$dir/demo.json" ]; then
		setup
		return
	fi
	cli=$(codeql_cli) || die "CodeQL ${pin%.*}.x is not installed; run make demo-check"
	"$bin" demo init --dir "$dir" --replace
	install_packs "$cli"
	say "a new dataset; tutorial progress and the saved Copilot credential start over"
}

case "${1:-}" in
check | setup | serve | login | reset) "$1" ;;
*) die "usage: scripts/demo.sh check|setup|serve|login|reset" ;;
esac
