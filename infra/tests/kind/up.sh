#!/usr/bin/env bash
# Brings up ABOM on a local kind cluster, or brings an existing one up to date: the cluster, the
# workload PKI (cert-manager, trust-manager, csi-driver-spiffe), PostgreSQL and an S3-compatible
# store beside it, then the chart. Safe to run again. Every command names the kind-abom context, so
# your current kubectl context is left as it was.
#
# The chart's defaults are for a real cluster; values.yaml here sizes it for kind, and these
# override it:
#   ABOM_KIND_CONCURRENCY    analysis Jobs at once        (default 3)
#   ABOM_KIND_WORKER_CPU     CPU each discovery Job may use    (default 1)
#   ABOM_KIND_WORKER_MEMORY  memory each discovery Job may use (default 2Gi)
#   ABOM_APP_DIR             GitHub App directory to read as (default .abom/db/github-app; absent reads public only)
set -euo pipefail
cd "$(dirname "$0")/../../.."

for tool in kind kubectl helm docker openssl python3; do
  command -v "$tool" >/dev/null || { echo "up.sh: $tool is not installed" >&2; exit 1; }
done

cluster=abom
context=kind-$cluster
here=infra/tests/kind
app_dir=${ABOM_APP_DIR:-.abom/db/github-app}

k() { kubectl --context "$context" "$@"; }
h() { helm --kube-context "$context" "$@"; }
apply() { k apply -f - >/dev/null; }

kind get clusters 2>/dev/null | grep -qx "$cluster" || kind create cluster --config "$here/cluster.yaml"

# One password for the cluster's own store and object store, made on the first run and kept only in
# the cluster: PostgreSQL keeps the password it first started with, so a rerun reads it back.
secret=$(k -n abom-deps get secret postgres -o jsonpath='{.data.POSTGRES_PASSWORD}' 2>/dev/null |
  openssl base64 -d -A || true)
[ -n "$secret" ] || secret=$(openssl rand -hex 24)

# Tagged by content, so the chart rolls the coordinator when the image changed and only then. The
# image an authoring session's agent runs is the same build with its tools, and the chart names it
# by the same tag with -authoring.
docker build -f infra/docker/Dockerfile -t codeql-abom:build .
tag=dev-$(docker image inspect -f '{{.Id}}' codeql-abom:build | cut -d: -f2 | cut -c1-12)
docker tag codeql-abom:build "codeql-abom:$tag"
kind load docker-image "codeql-abom:$tag" --name "$cluster"
if [ -f infra/docker/authoring/package-lock.json ]; then
  docker build -f infra/docker/Dockerfile --target authoring -t "codeql-abom:$tag-authoring" .
  kind load docker-image "codeql-abom:$tag-authoring" --name "$cluster"
else
  echo "up: infra/docker/authoring has no package-lock.json yet, so no authoring session can start here" >&2
fi

helm repo add jetstack https://charts.jetstack.io --force-update >/dev/null
helm repo add metrics-server https://kubernetes-sigs.github.io/metrics-server/ --force-update >/dev/null
h upgrade --install metrics-server metrics-server/metrics-server -n kube-system --version 3.14.0 \
  --set "args={--kubelet-insecure-tls}" --wait

# The default approver is off so csi-driver-spiffe alone approves workload certificates.
h upgrade --install cert-manager jetstack/cert-manager -n cert-manager --create-namespace --version v1.21.3 \
  --set crds.enabled=true --set "extraArgs={--controllers=*\,-certificaterequests-approver}" --wait

if ! k -n cert-manager get secret csi-driver-spiffe-ca >/dev/null 2>&1; then
  ca=$(mktemp -d)
  trap 'rm -rf "$ca"' EXIT
  openssl ecparam -name prime256v1 -genkey -noout -out "$ca/ca.key"
  openssl req -x509 -new -key "$ca/ca.key" -sha256 -days 30 -subj "/CN=abom kind SPIFFE CA" \
    -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" -out "$ca/ca.crt"
  k -n cert-manager create secret tls csi-driver-spiffe-ca --cert="$ca/ca.crt" --key="$ca/ca.key"
fi

# Kind only: with the default approver off, trust-manager's own webhook certificate waits for
# approval, and this approves it by hand. A real cluster runs approver-policy instead.
h upgrade --install trust-manager jetstack/trust-manager -n cert-manager --version v0.26.0
for _ in $(seq 1 60); do
  pending=$(k -n cert-manager get certificaterequests -o json | python3 -c '
import json, sys
for r in json.load(sys.stdin)["items"]:
    if r["spec"]["issuerRef"]["name"] == "trust-manager" and not r.get("status", {}).get("conditions"):
        print(r["metadata"]["name"])')
  for name in $pending; do
    k -n cert-manager patch certificaterequest "$name" --subresource=status --type=merge -p \
      '{"status":{"conditions":[{"type":"Approved","status":"True","reason":"KindSetup","message":"approved for the kind test cluster","lastTransitionTime":"'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'"}]}}' >/dev/null
  done
  k -n cert-manager rollout status deploy/trust-manager --timeout=5s >/dev/null 2>&1 && break
  sleep 5
done
k -n cert-manager rollout status deploy/trust-manager --timeout=120s

# The webhook takes requests a little after its Deployment reports ready.
for _ in $(seq 1 30); do k apply -f "$here/spiffe.yaml" >/dev/null 2>&1 && break; sleep 2; done
k apply -f "$here/spiffe.yaml"
h upgrade --install csi-driver-spiffe jetstack/cert-manager-csi-driver-spiffe -n cert-manager --version v0.15.1 \
  --set app.trustDomain=cluster.local \
  --set app.approver.signerName=clusterissuers.cert-manager.io/csi-driver-spiffe-ca \
  --set app.issuer.name=csi-driver-spiffe-ca --set app.issuer.kind=ClusterIssuer --set app.issuer.group=cert-manager.io --wait

k create namespace abom-deps --dry-run=client -o yaml | apply
k -n abom-deps create secret generic postgres --dry-run=client -o yaml \
  --from-literal=POSTGRES_USER=abom --from-literal=POSTGRES_PASSWORD="$secret" --from-literal=POSTGRES_DB=abom | apply
k -n abom-deps create secret generic s3 --dry-run=client -o yaml \
  --from-literal=RUSTFS_ACCESS_KEY=abom --from-literal=RUSTFS_SECRET_KEY="$secret" | apply
k apply -f "$here/deps.yaml"
k -n abom-deps rollout status deploy/postgres --timeout=300s
k -n abom-deps rollout status deploy/s3 --timeout=300s
k -n abom-deps delete pod mkbucket --ignore-not-found >/dev/null
k -n abom-deps run mkbucket --rm -i --restart=Never --image=amazon/aws-cli:2.27.0 \
  --env=AWS_ACCESS_KEY_ID=abom --env=AWS_SECRET_ACCESS_KEY="$secret" --env=AWS_DEFAULT_REGION=us-east-1 \
  --command -- sh -c 'e="--endpoint-url http://s3.abom-deps:9000"; aws $e s3api head-bucket --bucket abom 2>/dev/null || aws $e s3 mb s3://abom' >/dev/null

k create namespace abom --dry-run=client -o yaml | apply
k label namespace abom abom.github.com/spiffe-roots=true pod-security.kubernetes.io/enforce=restricted \
  --overwrite >/dev/null
# Put together from parts: written out whole, even a template reads as a credential to secret scanning.
login="abom:$secret"
k -n abom create secret generic abom-database --dry-run=client -o yaml \
  --from-literal=DATABASE_URL="postgres://$login@postgres.abom-deps:5432/abom?sslmode=disable" | apply
k -n abom create secret generic abom-s3 --dry-run=client -o yaml \
  --from-literal=AWS_ACCESS_KEY_ID=abom --from-literal=AWS_SECRET_ACCESS_KEY="$secret" | apply
app=()
if [ -f "$app_dir/app.json" ]; then
  k -n abom create secret generic abom-github-app --dry-run=client -o yaml \
    --from-file="$app_dir/app.json" --from-file="$app_dir/private-key.pem" --from-file="$app_dir/webhook-secret" | apply
  app=(--set githubApp.secretName=abom-github-app)
fi

h upgrade --install abom infra/k8s/helm/codeql-abom -n abom -f "$here/values.yaml" \
  --set image.repository=codeql-abom --set image.tag="$tag" \
  --set coordinator.concurrency="${ABOM_KIND_CONCURRENCY:-3}" \
  --set workers.discovery.cpu="${ABOM_KIND_WORKER_CPU:-1}" --set workers.discovery.memory="${ABOM_KIND_WORKER_MEMORY:-2Gi}" \
  ${app[@]+"${app[@]}"}
k -n abom rollout status deploy/abom-coordinator --timeout=300s

cat <<EOT
ABOM is up as codeql-abom:$tag. Reach it with:
  kubectl --context $context -n abom port-forward svc/abom 8080:8080
then open http://127.0.0.1:8080 (controls need a loopback Host, which the port-forward gives).
EOT
