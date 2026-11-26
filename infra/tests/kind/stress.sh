#!/usr/bin/env bash
# Crawls an organization on a larger kind cluster and records whether the coordinator and its store
# stay responsive while analysis Jobs use every core the workers are given. Meant for a machine with
# cores to spare, not a laptop.
#
#   ABOM_STRESS_ORG       organization to declare and crawl (required)
#   ABOM_STRESS_WORKERS   kind worker nodes, used only when creating the cluster (default 4)
#   ABOM_STRESS_MINUTES   how long to sample before pausing discovery (default 30)
#   ABOM_STRESS_INTERVAL  seconds between samples (default 15)
#   ABOM_STRESS_OUT       where samples and the summary go (default .abom/tmp/stress-<time>)
#   ABOM_STRESS_PORT      loopback port for the coordinator (default 18080)
# and up.sh's own: ABOM_KIND_CONCURRENCY, ABOM_KIND_WORKER_CPU, ABOM_KIND_WORKER_MEMORY, ABOM_APP_DIR.
set -euo pipefail
cd "$(dirname "$0")/../../.."

org=${ABOM_STRESS_ORG:?set ABOM_STRESS_ORG to the organization to crawl}
workers=${ABOM_STRESS_WORKERS:-4}
minutes=${ABOM_STRESS_MINUTES:-30}
interval=${ABOM_STRESS_INTERVAL:-15}
port=${ABOM_STRESS_PORT:-18080}
out=${ABOM_STRESS_OUT:-.abom/tmp/stress-$(date +%Y%m%d-%H%M%S)}
context=kind-abom
k() { kubectl --context "$context" "$@"; }

if ! kind get clusters 2>/dev/null | grep -qx abom; then
  config=$(mktemp)
  { printf 'kind: Cluster\napiVersion: kind.x-k8s.io/v1alpha4\nname: abom\nnodes:\n  - role: control-plane\n'
    for _ in $(seq 1 "$workers"); do printf '  - role: worker\n'; done; } >"$config"
  kind create cluster --config "$config"
  rm -f "$config"
fi
have=$(k get nodes --no-headers -l '!node-role.kubernetes.io/control-plane' | wc -l | tr -d ' ')
[ "$have" -ge "$workers" ] || {
  echo "stress.sh: the abom cluster has $have workers, not $workers; delete it with: kind delete cluster --name abom" >&2
  exit 1
}

infra/tests/kind/up.sh
mkdir -p "$out"
k -n abom exec deploy/abom-coordinator -c coordinator -- codeql-abom own --org "$org"

k -n abom port-forward svc/abom "$port:8080" >"$out/port-forward.log" 2>&1 &
forward=$!
base=http://127.0.0.1:$port
control() { curl -fsS -X POST -H 'X-Abom-Control: 1' "$base/api/discovery/$1" >/dev/null; }
# However the run ends, the crawl it started stops with it.
trap 'control pause 2>/dev/null || echo "stress.sh: discovery may still be running; pause it in the interface" >&2
  kill $forward 2>/dev/null || true' EXIT
for _ in $(seq 1 30); do curl -fsS "$base/api/summary" >/dev/null 2>&1 && break; sleep 1; done
control start

# Each sample: how long the coordinator takes to answer while Jobs run, which pods are using what,
# and the Jobs each pool holds. A coordinator slow to answer under load is what this is looking for.
echo "time,summary_seconds,status_seconds,jobs_running,jobs_pending_pods,coordinator_restarts" >"$out/samples.csv"
# A sample that cannot be read, such as metrics not yet collected for a new pod, is a gap in the
# record, not a reason to stop recording.
set +e
end=$(( $(date +%s) + minutes * 60 ))
while [ "$(date +%s)" -lt "$end" ]; do
  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  # curl writes the time even when the request fails, so a failure replaces what it wrote.
  summary=$(curl -o /dev/null -fsS -w '%{time_total}' "$base/api/summary" 2>/dev/null) || summary=fail
  if status=$(curl -fsS -w '\n%{time_total}' "$base/api/discovery/status" 2>/dev/null); then
    body=$(echo "$status" | sed '$d') took=$(echo "$status" | tail -1)
  else
    body='{}' took=fail
  fi
  printf '{"time":"%s","status":%s}\n' "$now" "$body" >>"$out/discovery.jsonl"
  running=$(k -n abom get jobs -l app.kubernetes.io/component=worker -o json |
    python3 -c 'import json,sys; print(sum(1 for j in json.load(sys.stdin)["items"] if j.get("status",{}).get("active")))')
  pending=$(k -n abom get pods -l app.kubernetes.io/component=worker --field-selector=status.phase=Pending --no-headers 2>/dev/null | wc -l | tr -d ' ')
  restarts=$(k -n abom get pods -l app.kubernetes.io/component=coordinator \
    -o jsonpath='{range .items[*]}{.status.containerStatuses[0].restartCount}{end}')
  echo "$now,$summary,$took,$running,$pending,${restarts:-0}" >>"$out/samples.csv"
  { echo "== $now"; k top nodes --no-headers 2>/dev/null; k top pods -n abom --no-headers 2>/dev/null
    k top pods -n abom-deps --no-headers 2>/dev/null; } >>"$out/top.txt"
  sleep "$interval"
done

set -e
control pause
k -n abom get events --sort-by=.lastTimestamp >"$out/events.txt"
k -n abom get pods -o wide >"$out/pods.txt"
python3 - "$out/samples.csv" <<'PY' | tee "$out/summary.txt"
import csv, statistics, sys
rows = list(csv.DictReader(open(sys.argv[1])))
def seconds(col):
    return sorted(float(r[col]) for r in rows if r[col] not in ("", "fail"))
for col in ("summary_seconds", "status_seconds"):
    v = seconds(col)
    failed = sum(1 for r in rows if r[col] == "fail")
    if v:
        p95 = v[min(len(v) - 1, int(len(v) * 0.95))]
        print(f"{col}: median {statistics.median(v):.3f}s, p95 {p95:.3f}s, max {v[-1]:.3f}s, {failed} unanswered of {len(rows)}")
    else:
        print(f"{col}: never answered in {len(rows)} samples")
def most(col):
    return max((int(r[col]) for r in rows if r[col].isdigit()), default="unknown")
print("most Jobs running at once:", most("jobs_running"))
print("most worker pods pending at once:", most("jobs_pending_pods"))
print("coordinator restarts:", rows[-1]["coordinator_restarts"] if rows else "unknown")
PY
echo "Samples, top and events are in $out."
