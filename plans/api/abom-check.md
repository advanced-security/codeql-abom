# A client in the workflow, thin enough to trust

**Status:** not built, and deferred by decision rather than by obstacle. This file is where the
design is refined until it is ready to be ranked; the verdict it would ask for is
[`verdict-gate.md`](verdict-gate.md).

## Why this exists

A verdict endpoint nobody calls changes nothing. Something has to run inside the pull request, ask
for the verdict at that commit, and turn the answer into a check a branch protection rule can
require. That is `abom-check`, and it is one piece of work rather than a line in the gate's plan
because of an asymmetry nothing else in this project has: **it is the only component that runs on
someone else's machine, in someone else's repository, under a token ABOM does not control.**

Everything else here can be fixed by restarting a process. A published action is pinned by its
consumers, so a bad release stays live in every workflow that references it until each one is
changed — by people who do not work for whoever shipped it.

It stays in-tree and local until it is mature. Splitting it into `advanced-security/abom-check`
first sounds tidier and is not: it makes testing a one-line change a round trip through three
repositories — edit the action, tag it, update the consuming workflow, run it — which is a tax paid
on every iteration for a benefit that only arrives at release.

## Pending

1. **The input and output contract.** Where the server is, which commit, which base, how long to
   wait, and which way inconclusive falls. Written down before anything is built, because this is
   the part consumers pin.
2. **It analyzes nothing.** It asks and it renders, which makes it the third client of the same API
   as the interface and the CLI, per [`api-first-invariant.md`](api-first-invariant.md). Depends on
   the verdict endpoint in [`verdict-gate.md`](verdict-gate.md).
3. **What the check run says.** The policy rule and the evidence, rendered separately, so a reader
   who disagrees can tell which of the two they disagree with. Depends on 1.
4. **Credentials in both directions.** It needs something to reach the ABOM server with, and it must
   not be handed anything that can change the repository it is running in.
5. **Its own supply chain.** ABOM exists to measure transitive Actions dependencies, so shipping an
   action whose own dependency tree ABOM would flag is not available to us. A composite action of
   commit-pinned steps, or one static binary, and the choice is made on what ABOM's own queries say
   about the result.
6. **Packaging and the split** to `advanced-security/abom-check` — release, tagging, and the
   commit-pinned usage the documentation will recommend. Last, and only once 1 through 5 hold.

## The constraints that must not be broken

**The action analyzes nothing.** A check that triggers analysis makes pull request latency a
function of the supply chain's depth, which is unbounded and not the contributor's fault.

**Inconclusive is never silently a pass.** A check that passes because it could not reach the server
manufactures confidence, which is worse than no check. Which way it falls is stated in the policy,
never defaulted to whichever is quieter.

**It holds no credential that can change the repository it runs in.** A gate needs to read a verdict
and write a check run. Anything beyond that is a way to turn a security control into an attack path,
in a workflow triggered by a pull request.

**Nothing ships until the contract is stable.** A released action is pinned by its consumers, and a
correction does not reach them; they have to come and get it.

## Verification

- ABOM's own queries run against the action's own source, and the result is what decides its
  packaging rather than a preference stated here.
- It runs against a server on this workstation, from a workflow on this workstation, and produces a
  check run whose summary names the policy and the evidence separately.
- Fail-open and fail-closed are each exercised against an unanalyzed head commit, and each produces
  the documented outcome.
- The token it is given cannot write to the repository, demonstrated by the write failing rather
  than by reading the permission block.
