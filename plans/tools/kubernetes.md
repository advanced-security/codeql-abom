# ABOM on Kubernetes, past kind

**Status:** Built and drilled from zero on a three-node kind cluster: a coordinator and three worker
pools of Jobs, each its own ServiceAccount and SPIFFE identity, under Pod Security `restricted`,
enforced NetworkPolicies and signed task grants, with sign-in and a read-only root filesystem on every
pod; stopping, rolling out, rolling back and uninstalling were drilled with Jobs running. The EKS
profile renders once filled in; nothing here has run on a cloud.

## Why this exists

A hosted instance runs CodeQL in parallel without the coordinator, which holds the store and signs
every decision, competing with it. [Deploy on Kubernetes](../../docs/deployment/kubernetes.md) says how
to run it; this plan holds what stands between that and a production deployment.

## Pending

1. **EKS, end to end.** Follow the [runbook](../../docs/deployment/kubernetes.md#on-eks) on a real
   account, fixing it wherever it is wrong, through every item of its checklist. The KMS signer and
   per-pool IAM roles are exercised here first. Depends on an AWS account.
2. **Scale.** Analysis at the concurrency a real organization needs, on a machine larger than a
   laptop: coordinator and database latency under load, priority and eviction holding the control
   plane above the workers, and how many Jobs one coordinator sustains. Measured with
   `infra/tests/kind/stress.sh`, then the pool defaults set from it. Depends on nothing ABOM builds.
3. **Published images and chart.** Today you build the one image yourself. Decide whether it splits
   into interface, coordinator and worker images, then publish multi-architecture images by digest
   with SBOMs and signatures, and the chart as an OCI artifact naming them. Depends on the first
   regular [release](releases.md).

## The constraints that must not be broken

**Only the coordinator holds the store.** No worker opens the database or receives its credential;
results reach it as signed attestations through the object store.

**A pool's identity confines it.** Each pool's ServiceAccount, SPIFFE identity, object-store prefix
and cloud role reach its own attempts and nothing else, so one pool's worker cannot read or forge
another's.

**Nothing is exposed without sign-in.** Without it the coordinator listens on loopback and only a
port-forward reaches it; the chart refuses an ingress otherwise.

**Health is the process answering.** A paused crawl, GitHub's availability or an expensive summary
never decides liveness.

## Verification

- Every item of the runbook's checklist on EKS.
- Under load on a large machine, the coordinator's API latency stays within its budget while workers
  saturate their nodes, and nothing the coordinator or database runs is evicted.
