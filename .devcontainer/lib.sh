# shellcheck shell=bash
# Sourced by the lifecycle scripts that reach the network while a Codespace is created.

# retry runs a step that reaches the network, up to four times. In a Codespace a lifecycle command
# that fails leaves a recovery container holding none of these tools, so one dropped connection
# would cost the whole Codespace.
retry() {
	local attempt
	for attempt in 1 2 3 4; do
		"$@" && return 0
		printf '%s: attempt %s of 4 failed: %s\n' "$(basename "$0" .sh)" "$attempt" "$*" >&2
		[ "$attempt" = 4 ] || sleep $((attempt * ${ABOM_RETRY_DELAY:-10}))
	done
	return 1
}
