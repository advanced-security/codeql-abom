#!/usr/bin/env bash
# Every start of this Codespace begins a fresh Demo: a new dataset in a private directory under
# /tmp, which a stop clears and which this clears again after a rebuild. A Demo that does not start
# leaves the Codespace's start succeeding, because Codespaces skips the status each attach shows,
# which says why, after a postStartCommand that fails.
set -euo pipefail
bash "$(dirname "$0")/start-demo.sh" --fresh ||
	echo "post-start: the Demo did not start; the status each attach shows says why and what to run"
