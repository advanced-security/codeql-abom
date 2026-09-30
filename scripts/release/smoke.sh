#!/usr/bin/env bash
#
# Proves a release archive works on the platform this runs on, from its own extracted bytes.
#
#   smoke.sh <archive.tar.gz>
#
# Needs the CodeQL CLI the release pins on PATH, and network access for `codeql pack install`.
# The server runs from the extracted directory: running an installation from any other working
# directory is not implemented yet, and this does not claim it.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
archive="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
name=$(basename "$archive" .tar.gz)
scratch=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/abom-smoke.XXXXXX")
server=""

stop_server() {
	if [ -n "$server" ]; then
		kill -TERM "$server" 2>/dev/null || true
		wait "$server" 2>/dev/null || true
		server=""
	fi
}
trap stop_server EXIT

tar -C "$scratch" -xzf "$archive"
root="$scratch/$name"
if [ ! -x "$root/codeql-abom" ]; then
	echo "smoke: $(basename "$archive") does not unpack to $name/codeql-abom" >&2
	exit 1
fi

echo "smoke: the archive matches its manifest"
(cd "$root" && python3 "$here/build_info.py" verify)

port=$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
base="http://127.0.0.1:$port"
mkdir -p "$scratch/state" "$scratch/gh"

start_server() {
	# No GitHub credential, so nothing this proves depends on one.
	(cd "$root" && exec env GH_TOKEN= GITHUB_TOKEN= GH_CONFIG_DIR="$scratch/gh" \
		./codeql-abom serve --db "$scratch/state/abom.db" --addr "127.0.0.1:$port" \
		--ui-dir ui --work-dir "$scratch/state/work" \
		--declare-file "$scratch/state/abom.local.json" >>"$scratch/serve.log" 2>&1) &
	server=$!
	for _ in $(seq 1 60); do
		if curl -fsS "$base/api/discovery/status" >/dev/null 2>&1; then
			return 0
		fi
		sleep 1
	done
	echo "smoke: the server did not answer; its log:" >&2
	cat "$scratch/serve.log" >&2
	return 1
}

echo "smoke: the server starts, serves the interface and reopens its database"
start_server
curl -fsS "$base/" | grep -q "<title>ABOM" || {
	echo "smoke: / did not serve the interface" >&2
	exit 1
}
stop_server
start_server
curl -fsS "$base/api/discovery/status" | python3 -c 'import json, sys; json.load(sys.stdin)'
stop_server

echo "smoke: the shipped packs resolve and ABOM's own queries report"
find "$root/ql" -name codeql-pack.yml | sort | while IFS= read -r manifest; do
	if ! codeql pack install "$(dirname "$manifest")" >>"$scratch/pack-install.log" 2>&1; then
		cat "$scratch/pack-install.log" >&2
		exit 1
	fi
done

mkdir -p "$scratch/fixture/.github/workflows"
cat >"$scratch/fixture/.github/workflows/smoke.yml" <<'EOF'
name: smoke
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: octo-org/example-action@v1
EOF
codeql database create "$scratch/db" --language=actions --source-root "$scratch/fixture" \
	--quiet >/dev/null

codeql database analyze "$scratch/db" \
	advanced-security/abom-actions-findings-src:codeql-suites/abom-experimental-findings.qls \
	--additional-packs "$root/ql" --format=sarif-latest --output "$scratch/findings.sarif" \
	--quiet >/dev/null
python3 - "$scratch/findings.sarif" <<'EOF'
import json
import sys

runs = json.load(open(sys.argv[1], encoding="utf-8"))["runs"]
ours = [r for run in runs for r in run.get("results", []) if r.get("ruleId", "").startswith("abom/")]
if not ours:
    sys.exit("smoke: ABOM's own findings queries reported nothing")
print(f"smoke: {len(ours)} results from ABOM's own findings queries")
EOF

codeql database run-queries "$scratch/db" \
	advanced-security/abom-actions-graph-extraction-src:codeql-suites/abom-dependency-graph.qls \
	--additional-packs "$root/ql" --quiet >/dev/null
rows=0
while IFS= read -r bqrs; do
	n=$(codeql bqrs info --format=json "$bqrs" |
		python3 -c 'import json, sys; print(sum(s["rows"] for s in json.load(sys.stdin)["result-sets"]))')
	rows=$((rows + n))
done < <(find "$scratch/db/results" -path '*abom-actions-graph-extraction-src*' -name '*.bqrs')
if [ "$rows" -eq 0 ]; then
	echo "smoke: the dependency graph queries extracted nothing" >&2
	exit 1
fi
echo "smoke: $rows rows from the dependency graph queries"

# Kept on failure, for reading; removed once there is nothing to read it for.
rm -rf "$scratch"
echo "smoke: $name passed"
