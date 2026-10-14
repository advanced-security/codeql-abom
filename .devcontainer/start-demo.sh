#!/usr/bin/env bash
# Start, or restart, the Demo: the loopback-only ABOM server over a private practice dataset, and,
# where the firewall can confine it, Copilot authoring through the allowlisting egress proxy.
#
#   start-demo.sh           restart, keeping the current exercise
#   start-demo.sh --fresh   discard it and start a new dataset
#   start-demo.sh --stop    stop it, keeping the exercise for the next start
set -euo pipefail
# The Demo's user reads what this script's user writes, such as the egress proxy's certificate, so
# whoever starts it must not decide through their umask whether it can.
umask 022

root=/tmp/abom-demo
logs=/tmp/abom-demo-logs
ca=$logs/egress-ca.pem
port=8099
egress_port=8098
bin=/opt/abom-demo/codeql-abom
tools=/opt/abom-demo/tools/node_modules/.bin
# AI credits, a cent each, one authoring session may spend: whoever opens a Codespace may never have
# read that a session spends theirs. 0 removes the limit, and Copilot refuses one below 30.
credits=${ABOM_DEMO_CREDITS:-150}
# Empty runs ABOM's default model, which not every Copilot plan offers.
model=${ABOM_DEMO_MODEL:-}
here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")

# Named rather than inherited: no token, proxy or helper in this shell reaches the Demo.
as_demo() {
	sudo -u abom-demo -H env -i PATH="$PATH" HOME=/home/abom-demo LANG=C.UTF-8 \
		sh -c 'umask 007; exec "$@"' sh "$@"
}

# demo serve refuses to start over a certificate its user cannot read, so authoring is offered only
# once the Demo's user has read the whole certificate.
readable_by_demo() { as_demo grep -q 'END CERTIFICATE' "$1" 2>/dev/null; }

stop() {
	for name in serve egress; do
		if [ -f "$logs/$name.pid" ]; then
			sudo kill "$(cat "$logs/$name.pid")" 2>/dev/null || true
			rm -f "$logs/$name.pid"
		fi
	done
}

# The Demo's user may reach loopback and nothing else, proven by trying rather than assumed. A
# Codespace runs this container on its VM's network, where loopback also holds the VM's DNS
# resolver, so name lookups are refused before loopback is admitted.
confine() {
	command -v iptables >/dev/null && sudo -n true 2>/dev/null || return 1
	local uid
	uid=$(id -u abom-demo)
	for table in iptables ip6tables; do
		sudo "$table" -w -N ABOM_DEMO 2>/dev/null || sudo "$table" -w -F ABOM_DEMO || continue
		for proto in udp tcp; do
			sudo "$table" -w -A ABOM_DEMO -p "$proto" --dport 53 -j REJECT
		done
		sudo "$table" -w -A ABOM_DEMO -o lo -j ACCEPT
		sudo "$table" -w -A ABOM_DEMO -j REJECT
		sudo "$table" -w -C OUTPUT -m owner --uid-owner "$uid" -j ABOM_DEMO 2>/dev/null ||
			sudo "$table" -w -A OUTPUT -m owner --uid-owner "$uid" -j ABOM_DEMO
	done
	if as_demo getent hosts api.github.com >/dev/null; then
		return 1
	fi
	for family in -4 -6; do
		if as_demo curl "$family" -s --max-time 5 -o /dev/null https://api.github.com/; then
			return 1
		fi
	done
}

mkdir -p "$logs"
chmod 0755 "$logs"
stop
if [ "${1:-}" = "--stop" ]; then
	echo "start-demo: the Demo is stopped; .devcontainer/start-demo.sh starts it again"
	exit 0
fi
if [ "${1:-}" = "--fresh" ] || [ ! -f "$root/demo.json" ]; then
	sudo rm -rf "$root"
	sudo install -d -o abom-demo -g abom-demo -m 2770 "$root"
	as_demo "$bin" demo init --dir "$root" --sources "$repo/testdata/corpus" \
		--pack-locks /opt/abom-demo/pack-locks
fi
# A stop cleared the last Demo's credential; a sign-in from copilot-login.sh outlives it.
ABOM_DEMO_ROOT="$root" bash "$here/copilot-login.sh" --if-signed-in ||
	echo "start-demo: the stored GitHub sign-in was not given to the Demo; run .devcontainer/copilot-login.sh"

authoring=()
if confine; then
	rm -f "$ca"
	setsid "$bin" demo egress --addr "127.0.0.1:$egress_port" --ca-out "$ca" \
		>>"$logs/egress.log" 2>&1 &
	echo $! >"$logs/egress.pid"
	for _ in $(seq 1 20); do
		# A default ACL on the directory overrides the umask, so the certificate is opened here.
		if [ -s "$ca" ]; then chmod 0644 "$ca" 2>/dev/null || :; fi
		readable_by_demo "$ca" && break
		sleep 0.5
	done
	if ! readable_by_demo "$ca"; then
		echo "start-demo: the Demo's user cannot read the egress proxy's certificate" \
			"($(stat -c '%a %U %n' "$logs" "$ca" 2>&1 | paste -sd ';' -)), so Copilot authoring stays off" |
			tee -a "$logs/serve.log"
		stop
	elif [ -x "$tools/copilot" ] && [ -x "$tools/codeql-development-mcp-server" ]; then
		authoring=(--copilot-cli "$tools/copilot" --ql-mcp "$tools/codeql-development-mcp-server"
			--egress-proxy "http://127.0.0.1:$egress_port" --egress-ca "$ca"
			--authoring-credits "$credits")
		[ -z "$model" ] || authoring+=(--authoring-model "$model")
	fi
else
	echo "start-demo: the Demo could not be confined to loopback, so Copilot authoring stays off" |
		tee -a "$logs/serve.log"
fi

hosts=""
if [ -n "${CODESPACE_NAME:-}" ] && [ -n "${GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN:-}" ]; then
	hosts="$CODESPACE_NAME-$port.$GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN"
	echo "start-demo: admitting the Codespace's forwarded address, $hosts"
elif [ "${CODESPACES:-}" = true ]; then
	echo "start-demo: nothing here names the Codespace's forwarded address, so a browser's requests" \
		"will be refused; restart the Demo from a terminal with .devcontainer/start-demo.sh"
fi
setsid sudo -u abom-demo -H env -i PATH="$PATH" HOME=/home/abom-demo LANG=C.UTF-8 \
	sh -c 'umask 007; exec "$@"' sh \
	"$bin" demo serve --dir "$root" --addr "127.0.0.1:$port" --ui-dir /opt/abom-demo/ui/wwwroot \
	--codeql /opt/codeql/codeql --allow-host "$hosts" "${authoring[@]}" >>"$logs/serve.log" 2>&1 &
serve=$!
echo "$serve" >"$logs/serve.pid"

for _ in $(seq 1 30); do
	if curl -fs --max-time 2 -o /dev/null "http://127.0.0.1:$port/api/instance"; then
		if [ -n "$hosts" ]; then
			echo "start-demo: ABOM Demo is serving at https://$hosts/ (\"ABOM Demo\" in the Ports panel)"
		else
			echo "start-demo: ABOM Demo is serving on port $port; open it from the Ports panel"
		fi
		exit 0
	fi
	[ -d "/proc/$serve" ] || break
	sleep 1
done
if [ -d "/proc/$serve" ]; then
	echo "start-demo: the Demo did not answer within 30s; see $logs/serve.log" >&2
else
	echo "start-demo: the Demo stopped as it started; see $logs/serve.log" >&2
fi
tail -n 20 "$logs/serve.log" >&2
exit 1
