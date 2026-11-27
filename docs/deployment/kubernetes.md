# Deploy on Kubernetes

The Helm chart is [`infra/k8s/helm/codeql-abom`](../../infra/k8s/helm/codeql-abom). It runs on a local
kind cluster with `make kind-up`. [`infra/k8s/eks/values.yaml`](../../infra/k8s/eks/values.yaml) is
the Amazon EKS profile: it renders only once every `<...>` in it is filled in. No image is
published: build [`infra/docker/Dockerfile`](../../infra/docker/Dockerfile) and push it to your
registry.

## What runs

| Part | |
| --- | --- |
| Coordinator | One Deployment. It holds the database lease, signs every decision, serves the interface and API, and starts Jobs. It outranks Jobs for scheduling and eviction. |
| `discovery` Jobs | One per repository the crawl analyzes. |
| `qlaas-test` Jobs | One per Test of a query in your pack. |
| `qlaas-run` Jobs | One per repository a requested run holds no database for. |
| `authoring` Jobs | One per authoring session or draft, `workers.authoring.concurrency` at once (one by default), the rest waiting their turn. The worker runs beside the Copilot CLI, from the image built with `--target authoring`. A session begins only with a GitHub App that can mint its Copilot token. |
| Attestor | One beside every Job's worker. It alone holds the attempt's grant, the decision bundle, the pool's SPIFFE identity, the attempt's own key and the object store's credentials; it reads only what the grant binds, signs each report and the result, and reads nothing the worker or an agent wrote. The worker reaches it over a socket only the two mount. |
| Egress proxy | One Deployment, with a listener for each pool: the only way out of a Job's pod. Each tunnels to the hosts its pool names in `egressProxy.allow`, at public addresses, and refuses and logs everything else; a pool naming none has no listener. |
| PostgreSQL | `DATABASE_URL` in the `database.secretName` Secret. Only the coordinator holds it. |
| Object store | S3, or an S3-compatible store beside kind. Grants, packs and results pass through it. |
| Workload PKI | cert-manager, trust-manager and csi-driver-spiffe. Each pool runs as its own ServiceAccount and SPIFFE identity. Each attempt is lent a key of its own, named in its grant, and a report or result is accepted only signed by both its pool's identity and that key. |

Every pod runs as a non-root user with a read-only root filesystem. A Job writes only to its own
scratch volume and `/tmp`, so nothing an attempt does changes what the next one runs. The attestor
and the Copilot CLI run as native sidecars, so the chart needs Kubernetes 1.29 or later.

A Job's pod reaches DNS and its own pool's listener of the proxy, and whatever
`networkPolicy.workerEgress` adds, and nothing directly. An object store at an `objectStore.endpoint`
of its own is reached directly, as are the hosts named in `egressProxy.direct`; on S3, every listener
admits the bucket's own host and no other bucket's.

## On kind

`make kind-up` builds the image and brings up the cluster, its PKI, PostgreSQL, an object store and
the chart, and is safe to run again. Reach it with
`kubectl --context kind-abom -n abom port-forward svc/abom 8080:8080`; the port-forward gives the
loopback address controls require, and has to be restarted when the coordinator's pod is replaced.

To try sign-in, run the test issuer on this machine under the name both the pods and your browser
can reach. Pods resolve `host.docker.internal`; your browser has to be told it is `127.0.0.1`, for
example Chromium's `--host-resolver-rules="MAP host.docker.internal 127.0.0.1"`.

```bash
go run ./infra/tests/oidc-issuer --addr 127.0.0.1:9400 --issuer http://host.docker.internal:9400 \
  --person vera=ABOM.Viewer --person otto=ABOM.Operator
kubectl --context kind-abom -n abom create secret generic abom-oidc --from-literal=client-secret=kind-only
helm --kube-context kind-abom -n abom upgrade abom infra/k8s/helm/codeql-abom --reuse-values \
  --set signIn.mode=oidc --set signIn.issuer=http://host.docker.internal:9400 --set signIn.clientId=abom \
  --set signIn.clientSecretName=abom-oidc --set signIn.redirectUrl=http://127.0.0.1:8080/auth/callback
```

`make kind-up` turns sign-in off again. `infra/tests/kind/stress.sh` crawls an organization on a
larger cluster and records how quickly the coordinator answers while the workers are busy. It is for
a machine with cores to spare.

## On EKS

Nothing here provisions for you; each step says what must exist, in whichever tool you use. The
placeholders are `<ACCOUNT_ID>`, `<REGION>`, `<CLUSTER>`, `<ECR_REPO>`, `<RDS_ENDPOINT>`,
`<BUCKET>`, `<KMS_KEY_ARN>`, `<HOST>` and `<ACM_CERT_ARN>`.

1. **Cluster.** Private node groups: a system group for the coordinator and its dependencies, and
   a worker group, tainted for analysis, of the image's architecture. The VPC CNI with network
   policy enabled, so the chart's NetworkPolicies are enforced. IMDSv2 with a hop limit of 1, so a
   pod cannot borrow its node's role.
2. **Image.** `docker buildx build --platform linux/<arch> -f infra/docker/Dockerfile`, pushed to
   `<ECR_REPO>`, and set `image.repository` and `image.tag`. Prefer the digest the push prints.
3. **Database.** RDS PostgreSQL 17 at `<RDS_ENDPOINT>`, private, with
   `sslmode=verify-full`. Put its URL in the `abom-database` Secret under `DATABASE_URL`; only the
   coordinator mounts it.
4. **Object store.** `<BUCKET>` with public access blocked, SSE-KMS, and a lifecycle rule
   expiring `attempts/`. Set `objectStore.region`, `objectStore.bucket` and `objectStore.prefix`.
   Every attempt's objects live under `<prefix>attempts/<pool>/`, which is what confines a pool.
5. **Decision key.** An asymmetric KMS key of spec `ECC_NIST_P256` for `SIGN_VERIFY`, named in
   `decisions.kmsKey` as `<KMS_KEY_ARN>`; ABOM calls it in the region the ARN names. Naming a new
   key rotates: the next start trusts it and retires every other key still deciding. What those
   keys signed still verifies, and nothing they sign afterwards does. `audit rotate-key` refuses
   while a KMS key is named.
6. **Identities.** EKS Pod Identity associations, or IRSA with
   `serviceAccounts.<name>.annotations`. The EKS profile lets a Job's attestor reach the Pod
   Identity agent at `169.254.170.23` on port 80, directly rather than through the proxy; the rest
   of `169.254.0.0/16`, the instance metadata service among it, stays refused. With IRSA, name
   `sts.<region>.amazonaws.com` in `egressProxy.everyPool` instead. Each Job's pod asks the
   identity webhook to skip its worker and agent containers, so only the attestor is handed the
   role. One role each:
   - `abom-coordinator`: `s3:GetObject` and `s3:PutObject` on `<prefix>attempts/*`, and
     `kms:Sign` and `kms:GetPublicKey` on the decision key, and nothing else.
   - `abom-worker-discovery`, `abom-worker-qlaas-test`, `abom-worker-qlaas-run` and
     `abom-worker-authoring`: the same two S3 actions on their own pool's prefix only,
     `<prefix>attempts/discovery/*`, `<prefix>attempts/qlaas-test/*`,
     `<prefix>attempts/qlaas-run/*` and `<prefix>attempts/authoring/*`, and no KMS.
7. **Workload PKI.** cert-manager with its default approver off, trust-manager, csi-driver-spiffe
   and approver-policy, which approves trust-manager's own certificate where kind approves it by
   hand. The CA `ClusterIssuer` and trust `Bundle` are those in
   [`infra/tests/kind/spiffe.yaml`](../../infra/tests/kind/spiffe.yaml). Label the namespace
   `abom.github.com/spiffe-roots=true` and `pod-security.kubernetes.io/enforce=restricted`.
8. **Load balancer.** The AWS Load Balancer Controller, an ACM certificate `<ACM_CERT_ARN>` for
   `<HOST>`, and DNS for `<HOST>`. The profile's ingress is an internal ALB terminating TLS and
   health-checking `/healthz`; name its subnets in `networkPolicy.webFrom`. The chart refuses an
   ingress without sign-in, and one whose host is not the sign-in redirect's.
9. **Sign-in.** Either, as [configuration](configuration.md) describes:
   - Entra ID: a web application with redirect URI `https://<HOST>/auth/callback` and app roles
     `ABOM.Viewer`, `ABOM.Analyst`, `ABOM.Operator` and `ABOM.Admin`; its client secret in the
     `abom-oidc` Secret under `client-secret`; `signIn.mode: oidc`. Where an MFA or conditional
     access policy is required, this is the way.
   - GitHub: `signIn.mode: github` through the reading App, with `https://<HOST>/auth/callback`
     added as a redirect URI in its settings, a client secret generated there in the Secret
     `signIn.clientSecretName` names, and `signIn.roleMap`.
10. **GitHub Apps.** The reading App's directory in the Secret `githubApp.secretName` names. A
    publisher for query exports, installed on its one repository, in `publisherApp.secretName`.
    An installation that has not approved what the App asks for, such as `members: read` for
    GitHub sign-in, is named by `codeql-abom app installations`.
11. **Install.** Copy the EKS profile, fill in every `<...>`, and
    `helm upgrade --install abom infra/k8s/helm/codeql-abom -n abom -f <your copy>`. Installing
    creates two cluster-wide PriorityClasses, which needs the right to, or `priority.create=false`.
12. **Monitoring**, if you want it. Amazon Managed Service for Prometheus or kube-prometheus-stack,
    named in `networkPolicy.scrapeFrom`, scraping the probe port.

## Check it

What the kind cluster is drilled against, on EKS:

- The coordinator is ready and `/healthz` answers through the ALB.
- Signing in comes back to `https://<HOST>/`, and `/api/me` names you and your role.
- As a viewer, starting analysis is refused with the role it needs, and the refusal is in the log.
- The coordinator's log names the decision key and each pool's identity.
- A discovery Job runs as `abom-worker-discovery`, and its pod cannot reach the coordinator's port
  or the instance metadata service.
- A discovery Job's worker container holds no AWS credential, variable or token file, and its
  attestor does.
- `kubectl -n abom exec deploy/abom-coordinator -- codeql-abom audit verify` verifies every signed
  decision, grant and receipt, reading the database and the trust bundle from the pod's
  environment.

## Stop, restart and uninstall

A coordinator that stops deletes the Jobs it started, their Secrets with them, and one that starts
deletes any its predecessor left behind; what they were analyzing is asked again. An authoring
session working when its coordinator stops is interrupted, keeping what its worker had reported, and
one waiting its turn waits on. Stopping a session suspends its Job: the worker has its pod's grace
period to report, and what it changed lands. Discovery that was
running is left paused unless resume-on-start is on in Settings. Sessions are kept in the database,
so a rollout signs nobody out. `helm rollback` changes the image and not the database: an earlier
image starts against the schema a later one migrated to, since migrations only add, but nothing
promises it reads everything the later one wrote.

`helm uninstall` keeps the `abom-state` volume, which holds the decision key, so a reinstall in the
namespace verifies every decision already made. It leaves the Secrets you created and the
`abom-decision-trust` ConfigMap the coordinator publishes its public keys in. To remove ABOM
entirely, delete those and the volume as well.
