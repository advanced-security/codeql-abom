#!/usr/bin/env bash
# Samples how many CodeQL analysis processes overlap, without matching itself.
#
# Keep the grep pattern out of argv so the sampler does not count itself.
set -uo pipefail

seconds=${1:-90}
pattern='database create|database analyze|query run'

max=0
peak=""
for ((i = 0; i < seconds; i++)); do
	all=$(ps -eo pid,ppid,args= |
		grep -E "codeql.*($pattern)" |
		grep -v -e 'grep -E' -e 'codeql-abom')
	# Count only the root of each wrapper/JVM process chain.
	pids=$(echo "$all" | awk '{print $1}')
	lines=$(echo "$all" | while read -r pid ppid rest; do
		echo "$pids" | grep -qx "$ppid" || echo "$pid $ppid $rest"
	done)
	if [ -z "$lines" ]; then
		n=0
	else
		n=$(echo "$lines" | wc -l | tr -d ' ')
	fi
	if ((n > max)); then
		max=$n
		peak=$lines
	fi
	sleep 1
done

echo "peak concurrent CodeQL analysis invocations: $max"
if ((max > 1)); then
	echo "--- invocations at the peak ---"
	echo "$peak" | cut -c1-160
fi
