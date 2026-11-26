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
| PostgreSQL | `DATABASE_URL` in the `database.secretName` Secret. Only the coordinator holds it. |
| Object store | S3, or an S3-compatible store beside kind. Grants, packs and results pass through it. |
| Workload PKI | cert-manager, trust-manager and csi-driver-spiffe. Each pool runs as its own ServiceAccount and SPIFFE identity, and a result is accepted only as signed by its pool's. |

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
   `serviceAccounts.<name>.annotations`. The EKS profile lets workers reach the Pod Identity agent
   at `169.254.170.23` on port 80; the rest of `169.254.0.0/16`, the instance metadata service among
   it, stays refused. One role each:
   - `abom-coordinator`: `s3:GetObject` and `s3:PutObject` on `<prefix>attempts/*`, and
     `kms:Sign` and `kms:GetPublicKey` on the decision key, and nothing else.
   - `abom-worker-discovery`, `abom-worker-qlaas-test` and `abom-worker-qlaas-run`: the same two S3
     actions on their own pool's prefix only, `<prefix>attempts/discovery/*`,
     `<prefix>attempts/qlaas-test/*` and `<prefix>attempts/qlaas-run/*`, and no KMS.
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
     added as a callback URL in its settings, a client secret generated there in the Secret
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
- `kubectl -n abom exec deploy/abom-coordinator -- codeql-abom audit verify` verifies every signed
  decision, grant and receipt, reading the database and the trust bundle from the pod's
  environment.
