#!/usr/bin/env bash
# Start, or restart, the Demo: the loopback-only ABOM server over a private practice dataset, and,
# where the firewall can confine it, Copilot authoring through the allowlisting egress proxy.
#
#   start-demo.sh           restart, keeping the current exercise
#   start-demo.sh --fresh   discard it and start a new dataset
set -euo pipefail

root=/tmp/abom-demo
logs=/tmp/abom-demo-logs
port=8099
egress_port=8098
bin=/opt/abom-demo/codeql-abom
repo=$(cd "$(dirname "$0")/../.." && pwd)

# Named rather than inherited: no token, proxy or helper in this shell reaches the Demo.
as_demo() {
	sudo -u abom-demo -H env -i PATH="$PATH" HOME=/home/abom-demo LANG=C.UTF-8 \
		sh -c 'umask 007; exec "$@"' sh "$@"
}

stop() {
	for name in serve egress; do
		if [ -f "$logs/$name.pid" ]; then
			sudo kill "$(cat "$logs/$name.pid")" 2>/dev/null || true
			rm -f "$logs/$name.pid"
		fi
	done
}

# The Demo's user may reach loopback and nothing else; proven by trying, not assumed.
confine() {
	command -v iptables >/dev/null && sudo -n true 2>/dev/null || return 1
	local uid
	uid=$(id -u abom-demo)
	for table in iptables ip6tables; do
		sudo "$table" -w -N ABOM_DEMO 2>/dev/null || sudo "$table" -w -F ABOM_DEMO || continue
		sudo "$table" -w -A ABOM_DEMO -o lo -j ACCEPT
		sudo "$table" -w -A ABOM_DEMO -j REJECT
		sudo "$table" -w -C OUTPUT -m owner --uid-owner "$uid" -j ABOM_DEMO 2>/dev/null ||
			sudo "$table" -w -A OUTPUT -m owner --uid-owner "$uid" -j ABOM_DEMO
	done
	for family in -4 -6; do
		if as_demo curl "$family" -s --max-time 5 -o /dev/null https://api.github.com/; then
			return 1
		fi
	done
}

mkdir -p "$logs"
stop
if [ "${1:-}" = "--fresh" ] || [ ! -f "$root/demo.json" ]; then
	sudo rm -rf "$root"
	sudo install -d -o abom-demo -g abom-demo -m 2770 "$root"
	as_demo "$bin" demo init --dir "$root" --sources "$repo/testdata/corpus" \
		--pack-locks /opt/abom-demo/pack-locks
fi

authoring=()
if confine; then
	setsid "$bin" demo egress --addr "127.0.0.1:$egress_port" >>"$logs/egress.log" 2>&1 &
	echo $! >"$logs/egress.pid"
	copilot=$(command -v copilot || true)
	qlmcp=$(command -v codeql-development-mcp-server || true)
	if [ -n "$copilot" ] && [ -n "$qlmcp" ]; then
		authoring=(--copilot-cli "$copilot" --ql-mcp "$qlmcp" --egress-proxy "http://127.0.0.1:$egress_port")
	fi
else
	echo "start-demo: the Demo could not be confined to loopback, so Copilot authoring stays off" |
		tee -a "$logs/serve.log"
fi

hosts=""
if [ -n "${CODESPACE_NAME:-}" ] && [ -n "${GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN:-}" ]; then
	hosts="$CODESPACE_NAME-$port.$GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN"
fi
setsid sudo -u abom-demo -H env -i PATH="$PATH" HOME=/home/abom-demo LANG=C.UTF-8 \
	sh -c 'umask 007; exec "$@"' sh \
	"$bin" demo serve --dir "$root" --addr "127.0.0.1:$port" --ui-dir /opt/abom-demo/ui/wwwroot \
	--codeql /opt/codeql/codeql --allow-host "$hosts" "${authoring[@]}" >>"$logs/serve.log" 2>&1 &
echo $! >"$logs/serve.pid"

for _ in $(seq 1 30); do
	if curl -fsS -o /dev/null "http://127.0.0.1:$port/api/instance"; then
		echo "start-demo: ABOM Demo is serving on port $port; open it from the Ports panel"
		exit 0
	fi
	sleep 1
done
echo "start-demo: the Demo did not answer within 30s; see $logs/serve.log" >&2
tail -n 20 "$logs/serve.log" >&2
exit 1
