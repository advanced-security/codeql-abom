#!/usr/bin/env bash
#
# Lints and renders the Helm chart for each profile it ships, and checks that the refusals the
# templates make still refuse.
#
# Nothing else renders the chart before a cluster does, so a template error, or a value the schema
# should have refused, would otherwise first show up as an install that fails or, worse, succeeds.
set -uo pipefail

cd "$(dirname "$0")/.."

chart=infra/k8s/helm/codeql-abom
eks=infra/k8s/eks/values.yaml
fail=0
note() {
	echo "check-chart: $1"
	fail=1
}

command -v helm >/dev/null || {
	echo "check-chart: helm is not installed; run scripts/setup-helm.sh"
	exit 1
}

out=$(mktemp)
trap 'rm -f "$out"' EXIT

helm lint "$chart" >"$out" 2>&1 || { cat "$out"; note "the chart does not lint with its defaults"; }
# The EKS overlay as someone has filled it in; left as shipped, its placeholders are refused below.
filled=(-f "$eks" --set image.repository=111111111111.dkr.ecr.us-east-1.amazonaws.com/codeql-abom --set image.tag=1.0.0
	--set objectStore.region=us-east-1 --set objectStore.bucket=abom-state
	--set decisions.kmsKey=arn:aws:kms:eu-west-1:111111111111:key/k
	--set signIn.issuer=https://login.microsoftonline.com/tenant/v2.0 --set signIn.clientId=abom
	--set signIn.redirectUrl=https://abom.example/auth/callback --set ingress.host=abom.example
	--set 'networkPolicy.webFrom[0].ipBlock.cidr=10.0.0.0/16'
	--set-string 'ingress.annotations.alb\.ingress\.kubernetes\.io/certificate-arn=arn:aws:acm:us-east-1:111111111111:certificate/c')
helm lint "$chart" "${filled[@]}" >"$out" 2>&1 || { cat "$out"; note "the chart does not lint with $eks filled in"; }

# render name args... — the profile must render.
render() {
	local name=$1
	shift
	helm template abom "$chart" -n abom "$@" >"$out" 2>&1 || { cat "$out"; note "the $name profile does not render"; }
}

# refuse name want args... — the profile must fail to render, saying want.
refuse() {
	local name=$1 want=$2
	shift 2
	if helm template abom "$chart" -n abom "$@" >"$out" 2>&1; then
		note "$name rendered, and the chart should refuse it"
	elif ! grep -qF -- "$want" "$out"; then
		cat "$out"
		note "$name was refused without saying \"$want\""
	fi
}

oidc=(--set signIn.mode=oidc --set signIn.issuer=https://issuer.example --set signIn.clientId=abom
	--set signIn.clientSecretName=abom-oidc --set signIn.redirectUrl=https://abom.example/auth/callback)

render default
! grep -qE -- '-cpu-limit=$' "$out" || note "a pool with no CPU limit set renders an empty one"
# A worker's pod reaches DNS and its own pool's listener of the egress proxy, and nothing directly: a
# repository or an agent wrote what it reads. Each listener is reached from its own pool alone, admits
# the hosts that pool needs, and the coordinator tells the pool where it is.
policy() { awk -v name="$1" '/^---/ {on=0} $1 == "name:" && $2 == name {on=1} on' "$out"; }
listener() { awk -v name="$1" '/^---/ {on=0} /name: abom-egress-proxy$/ {on=1} on' "$out" | grep -A6 -- "- name: $1$"; }
for pool in discovery:3129:worker qlaas-test:3130:test-worker qlaas-run:3131:run-worker authoring:3128:authoring; do
	IFS=: read -r name port flag <<<"$pool"
	! policy "abom-worker-$name" | grep -qF '0.0.0.0/0' || note "a $name pod reaches the internet other than through the proxy"
	policy "abom-worker-$name" | grep -qF "ports: [{port: $port, protocol: TCP}]" || note "a $name pod does not reach its listener"
	policy abom-egress-proxy | grep -A1 -F "abom.github.com/pool: $name}}}]" | grep -qF "ports: [{port: $port, protocol: TCP}]" ||
		note "$name pods do not reach their listener alone"
	grep -qF -- "--$flag-egress-proxy=http://abom-egress-proxy:$port" "$out" || note "the coordinator does not tell $name its proxy"
done
for allow in discovery:github.com qlaas-test:abom.s3.us-east-1.amazonaws.com authoring:.githubcopilot.com; do
	listener "${allow%%:*}" | grep -qF -- "${allow#*:}" || note "the ${allow%%:*} listener does not admit ${allow#*:}"
done
listener qlaas-test | grep -qF github.com && note "the qlaas-test listener admits a host only discovery needs"
listener authoring | grep -qF 'github.com,' && note "the authoring listener admits a host only discovery needs"
[ "$(policy abom-egress-proxy | grep -c 'from:')" = 4 ] || note "the egress proxy is reached from other than the pools"
for want in --authoring-agent-image=codeql-abom:dev-authoring \
	--ql-mcp=codeql-development-mcp-server --authoring-worker-service-account=abom-worker-authoring; do
	grep -qF -- "$want" "$out" || note "the coordinator is not told $want"
done
! grep -qE -- '--authoring-(concurrency|credits)=' "$out" || note "sessions' concurrency or credits are set with nothing asking"
# The lock every CodeQL invocation takes is made in the user's cache directory, beneath the
# read-only HOME unless it is moved onto the coordinator's scratch.
grep -qF '{name: XDG_CACHE_HOME, value: /tmp/cache}' "$out" || note "the coordinator's cache directory is on its read-only root"
render eks "${filled[@]}"
# The key's own region signs, which need not be the bucket's.
grep -qF 'name: AWS_REGION, value: "eu-west-1"' "$out" || note "decisions are not signed in the KMS key's own region"
# EKS Pod Identity hands a worker its credentials from the node's agent, inside the refused link-local range.
grep -qF '169.254.170.23/32' "$out" || note "on EKS a worker cannot reach the Pod Identity agent for its credentials"
grep -qF -- '--allow=github.com,abom-state.s3.us-east-1.amazonaws.com' "$out" || note "on EKS no listener admits the bucket"
grep -qF -- '--worker-no-proxy=169.254.170.23,fd00:ec2::23' "$out" || note "on EKS a worker reaches its credentials through the proxy"
# Numbers as a values file gives them: 0 lifts a Job's CPU cap, and a priority is written as an integer.
render numbers --set-json workers.discovery.cpuLimit=0 --set-json priority.control=2000000
for want in --worker-cpu-limit=0 'value: 2000000'; do
	grep -qF -- "$want" "$out" || note "numeric values do not render $want"
done
render kind -f infra/tests/kind/values.yaml --set image.tag=dev-0123456789ab --set githubApp.secretName=abom-github-app
# An object store in the cluster is reached directly, and a pool with nothing else to reach has no listener.
grep -qF -- '--worker-no-proxy=s3.abom-deps' "$out" || note "on kind a worker reaches its object store through the proxy"
! grep -qF -- '--test-worker-egress-proxy=' "$out" || note "on kind the test pool, with nothing to reach, is given a proxy"
! policy abom-worker-qlaas-test | grep -qF 'port: 3130' || note "on kind the test pool reaches a listener it has not got"
render sessions --set workers.authoring.concurrency=2 --set-json workers.authoring.credits=0 \
	--set workers.authoring.image=registry.example/agent:1
for want in --authoring-concurrency=2 --authoring-credits=0 --authoring-agent-image=registry.example/agent:1; do
	grep -qF -- "$want" "$out" || note "the sessions profile does not render $want"
done
github=(--set signIn.mode=github --set signIn.clientSecretName=abom-github-signin
	--set signIn.redirectUrl=https://abom.example/auth/callback --set 'signIn.roleMap=acme/abom-operators=operator\,acme:owners=admin'
	--set githubApp.secretName=abom-github-app)
render github "${github[@]}" --set ingress.enabled=true --set ingress.host=abom.example \
	--set 'networkPolicy.webFrom[0].ipBlock.cidr=10.0.0.0/16'
for want in --auth=github --github-role-map=acme/abom-operators=operator,acme:owners=admin ABOM_GITHUB_CLIENT_SECRET \
	--addr=0.0.0.0:8080; do
	grep -qF -- "$want" "$out" || note "the github profile does not render $want"
done
# Every container ABOM runs, the App's init containers too, writes only to the volumes it mounts.
[ "$(grep -c 'readOnlyRootFilesystem: true' "$out")" = "$(grep -c 'allowPrivilegeEscalation: false' "$out")" ] ||
	note "a container of the github profile has a writable root filesystem"
render oidc "${oidc[@]}" --set ingress.enabled=true --set ingress.host=abom.example \
	--set 'networkPolicy.webFrom[0].ipBlock.cidr=10.0.0.0/16' \
	--set publisherApp.secretName=abom-publisher --set decisions.kmsKey=arn:aws:kms:us-east-1:111111111111:key/k

web=(--set 'networkPolicy.webFrom[0].ipBlock.cidr=10.0.0.0/16')
refuse "the EKS overlay with its placeholders left in" "does not match pattern '^[^<>]*\$'" -f "$eks"
refuse "a value the chart has no use for" "additional properties 'cpuLimit' not allowed" --set workers.qlaasTest.cpuLimit=3
refuse "an ingress without sign-in" "set signIn.mode to oidc or github" "${web[@]}" --set ingress.enabled=true --set ingress.host=abom.example
refuse "GitHub sign-in that grants nobody a role" "signIn.roleMap is required for github" "${github[@]}" --set signIn.roleMap=
refuse "GitHub sign-in without the App's client secret" "signIn.clientSecretName is required for github" "${github[@]}" \
	--set signIn.clientSecretName=
refuse "GitHub sign-in without the reading App" "githubApp.secretName is required for github" "${github[@]}" \
	--set githubApp.secretName=
refuse "an ingress on another host than sign-in returns to" "is not the host of signIn.redirectUrl" "${oidc[@]}" \
	"${web[@]}" --set ingress.enabled=true --set ingress.host=other.example
refuse "an ingress the network policy admits nothing to" "needs networkPolicy.webFrom" "${oidc[@]}" \
	--set ingress.enabled=true --set ingress.host=abom.example
refuse "a cluster too old for the attestor's native sidecar" "need Kubernetes 1.29 or later" --kube-version 1.28.0
refuse "a listener admitting a URL rather than a host" "does not match pattern" --set 'egressProxy.allow.discovery[0]=https://github.com'
refuse "a listener for a pool the chart has not got" "additional properties 'other' not allowed" --set 'egressProxy.allow.other[0]=github.com'
refuse "a credit limit that is not a number" "does not match pattern" --set-string workers.authoring.credits=lots

if [ $fail -eq 0 ]; then
	echo "check-chart: the chart lints, renders 7 profiles and refuses 12"
fi
exit $fail
