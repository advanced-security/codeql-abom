#!/usr/bin/env bash
# Runs groups of make targets side by side: the targets of a group one after another, the groups at
# once. Each group writes its own log, and what passed, what failed and how long each took is said as
# each one finishes. `make verify` and `make check` both run through this.
#
#   scripts/verify.sh name:target,target name:target ...
#
#   ABOM_VERIFY_JOBS  groups run at once (default: every group named; 1 runs them in turn)
#   ABOM_VERIFY_NICE  niceness the groups run at, so the machine stays usable (default 10)
#   ABOM_VERIFY_LOGS  directory for each group's log (default .abom/verify)
set -uo pipefail
cd "$(dirname "$0")/.."

[ "$#" -gt 0 ] || { echo "verify: name at least one group, as name:target,target" >&2; exit 2; }
make=${MAKE:-make}
logs=${ABOM_VERIFY_LOGS:-.abom/verify}
jobs=${ABOM_VERIFY_JOBS:-$#}
niceness=${ABOM_VERIFY_NICE:-10}
rm -rf "$logs"
mkdir -p "$logs"

pids=()
stop() {
	for pid in ${pids[@]+"${pids[@]}"}; do kill -TERM -- "-$pid" 2>/dev/null; done
	exit 130
}
trap stop INT TERM

took() { printf '%dm%02ds' $(($1 / 60)) $(($1 % 60)); }

run() {
	local name=$1 started
	shift
	started=$(date +%s)
	# -j1: a group's targets share what they build, such as the interface's one .NET project.
	nice -n "$niceness" "$make" -j1 --no-print-directory "$@" >"$logs/$name.log" 2>&1
	echo "$? $(($(date +%s) - started))" >"$logs/$name.result"
}

running() {
	local n=0 pid
	for pid in ${pids[@]+"${pids[@]}"}; do kill -0 "$pid" 2>/dev/null && n=$((n + 1)); done
	echo "$n"
}

names=()
reported=()
report() {
	local name status seconds
	for name in "${names[@]}"; do
		[ -f "$logs/$name.result" ] || continue
		[[ " ${reported[*]-} " == *" $name "* ]] && continue
		read -r status seconds <"$logs/$name.result"
		if [ "$status" -eq 0 ]; then
			echo "verify: $name passed in $(took "$seconds")"
		else
			echo "verify: $name FAILED in $(took "$seconds"); its log is $logs/$name.log"
		fi
		reported+=("$name")
	done
}

begun=$(date +%s)
for spec in "$@"; do
	name=${spec%%:*}
	IFS=, read -ra targets <<<"${spec#*:}"
	while [ "$(running)" -ge "$jobs" ]; do
		sleep 1
		report
	done
	echo "verify: $name runs ${targets[*]}"
	# Its own process group, so stopping this stops everything the group started. Only here: job
	# control around every short-lived command races bash's own setpgid and says so.
	set -m
	run "$name" "${targets[@]}" &
	pids+=($!)
	set +m
	names+=("$name")
done
while [ "${#reported[@]}" -lt "${#names[@]}" ]; do
	sleep 1
	report
done
wait

failed=()
for name in "${names[@]}"; do
	read -r status _ <"$logs/$name.result"
	[ "$status" -eq 0 ] || failed+=("$name")
done
for name in ${failed[@]+"${failed[@]}"}; do
	echo
	echo "verify: the end of $name's log:"
	tail -n 40 "$logs/$name.log"
done
echo
if [ "${#failed[@]}" -gt 0 ]; then
	echo "verify: ${failed[*]} failed; ${#names[@]} groups in $(took $(($(date +%s) - begun)))"
	exit 1
fi
echo "verify: ${#names[@]} groups passed in $(took $(($(date +%s) - begun)))"
