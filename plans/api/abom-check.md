# A client in the workflow, thin enough to trust

**Status:** not built; deliberately deferred while the client and server contracts are refined.

The verdict belongs to [verdict-gate.md](verdict-gate.md). This plan owns its workflow client,
including tests, permissions, documentation and release. Submission expansion is deferred in
[dependency-submission.md](dependency-submission.md) pending evidence of a gap in native GitHub
capabilities; it is not part of this action's planned release.

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

## Scope

The planned action asks for the operator's policy verdict at exact base and head commits and renders
the answer. Native Actions lockfiles are the server's resolution baseline: each side must use its
own governing lock context, not today's tag or the current default branch's lockfile. A lockfile
parse, offline coverage check, network verification and observed runner enforcement are different
evidence, and the summary must not collapse them into one claim that the workflow is safe.

GitHub's tooling owns lock generation, verification and updates. The action does not run those
operations, collect dependencies or submit snapshots. Existing server preview/submission controls
remain available to authorized operators; they are not workflow-client operations in this plan.

## Pending

1. **The input and output contract.** Name the server endpoint, repository, exact head
   and base SHAs, event/ref context, timeout, response schema and structured outputs. The server
   echoes the identity and resolution coverage of the evidence it used; the client refuses a
   different repository, commit or lock context. Unavailable, stale, unauthorized, inconclusive
   and failed are distinct outcomes.
   Inconclusive follows the operator's explicit policy choice. Keep metadata defaults,
   entrypoint behavior and documented examples under the same contract tests.
2. **Reuse the server, never reconstruct its answer.** The client is the third caller of the same
   API as the interface and CLI, per [api-first-invariant.md](api-first-invariant.md). It needs the
   verdict endpoint and bounded read retries, not a new parser, verifier or policy evaluator.
   Missing analysis or unresolved lock semantics is an explicit incomplete answer, not a request
   to start analysis or substitute live-tag results. Depends on 1 and the verdict endpoint.
3. **What the check and summary say.** Render the policy rule and evidence separately, including
   locked versus unlocked coverage and unknown verification. Preserve the distinction between
   immutable commit selection and advisory-version evidence. Mask credentials and avoid logging
   authorization headers or raw credentials in URLs. Depends on 1.
4. **Credentials in both directions.** Use the authenticated workflow-caller contract in
   [org-server.md](org-server.md): a short-lived grant scoped to repository, operation and trusted
   event/ref. The grant permits verdict reads, never submission, discovery or global renewal.
   Validate fork pull-request identity without granting the fork access to another repository's
   evidence. If the client writes check runs, justify that permission separately from
   repository-content access. No GitHub contents-write credential reaches the action. Depends on
   1 and the server's read authorization path, not a new submission grant.
5. **Its own supply chain.** ABOM exists to measure transitive Actions dependencies, so shipping an
   action whose own dependency tree ABOM would flag is not available to us. Compare a composite
   action and a static binary using measured native-lock coverage, platform compatibility and
   ABOM's own queries. An inline tag backed by a verified governing lock is not equivalent to an
   unlocked tag; an action repository's lock must not be assumed to govern its callers. Test the
   packaged entrypoint, document platform support and avoid unpinned runtime installation.
6. **Packaging and the split** to `advanced-security/abom-check` — release, tagging, and the
   native-lock-backed usage the documentation will recommend on supported platforms. Document
   explicit SHA pinning where native enforcement is unavailable without claiming equivalent
   transitive coverage. Require reproducible artifacts, a check that published artifacts match
   reviewed source, and minimum-permission examples.
   Last, and only once 1 through 5 hold; adopting these practices does not prescribe a TypeScript
   implementation or a checked-in JavaScript bundle.

## The constraints that must not be broken

**The action analyzes nothing.** A check that triggers analysis makes pull request latency a
function of the supply chain's depth. Cloning, CodeQL execution, dependency traversal, policy
evaluation and snapshot construction remain server responsibilities.

**Inconclusive is never silently a pass.** A check that passes because it could not reach the server
manufactures confidence, which is worse than no check. Which way it falls is stated in the policy,
never defaulted to whichever is quieter.

**The client has no submission authority.** The action holds no GitHub credential that can write
repository contents, and its server grant permits only authorized reads. `X-Abom-Control` and
`--allow-remote-control` are not authentication. A verdict request neither submits a snapshot nor
rewrites, relocks or accepts a moved dependency.

**Nothing ships until the contract is stable.** A released action is pinned by its consumers, and a
correction does not reach them; they have to come and get it.

## Verification

- ABOM's own queries run against the action's own source, and the result is what decides its
  packaging rather than a preference stated here.
- It runs against a server on this workstation, from a workflow on this workstation, and produces a
  check run whose summary names the policy and the evidence separately.
- Fail-open and fail-closed are each exercised against an unanalyzed head commit, and each produces
  the documented outcome.
- Contract tests cover defaults, schema mismatch, wrong repository/SHA/lock context, timeouts,
  cancellation, stale evidence and secret redaction through the packaged entrypoint. Assert no
  cloning, CodeQL, discovery, lockfile mutation or dependency-submission calls.
- A fake server proves wrong-target, expired and untrusted-event grants cannot read protected
  evidence, and that a valid verdict grant has no submission or discovery authority.
- In a dedicated approved test repository, the client's GitHub credential cannot write contents,
  while its permitted check-run operation works. Demonstrate the refusal, not just the permission
  block. Locked, explicitly unlocked and broken-lock inputs remain distinguishable in the summary.

## Prior art

Jesse Houwing's
[actions-dependency-submission](https://github.com/jessehouwing/actions-dependency-submission),
introduced in [issue #18](https://github.com/advanced-security/codeql-abom/issues/18), is useful
prior art for a workflow-facing contract and integration tests. Its
[action metadata](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/action.yml),
[entrypoint tests](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/__tests__/main.test.ts)
and [artifact check](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/.github/workflows/check-dist.yml)
inform the action-contract acceptance criteria here, not a commitment to implement another
publisher. GitHub's [native locking tool](https://github.com/github/gh-actions-lock/blob/v0.1.6/README.md)
owns dependency pinning and verification. Credit both projects in the released documentation;
retain the required MIT notices if a later implementation incorporates source or test material.
