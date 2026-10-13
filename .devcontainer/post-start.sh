#!/usr/bin/env bash
# Every start of this Codespace begins a fresh Demo: a new dataset in a private directory under
# /tmp, which a stop clears and which this clears again after a rebuild.
set -euo pipefail
exec bash "$(dirname "$0")/start-demo.sh" --fresh
