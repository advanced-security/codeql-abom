# A gate that blocks, and can say whose rule it was

**Status:** not built. A gate is only a gate once it runs in somebody's CI, which needs a server for
it to ask and a client to ask from — and both stay on this workstation until they are mature. The
policy design is settled and stated below. **Drafting, simulating and adopting a policy is built**:
`store.EvaluatePolicy` is the evaluation this gate must reuse rather than reimplement, and
`store.SelectorFor` is the one path from predicate to selector. The client is
[`abom-check.md`](abom-check.md).

## Why this exists

Reporting a newly reachable vulnerable dependency after it has merged is a worse product than
refusing the pull request that introduced it. ABOM already relates an organization's transitive
Actions dependencies to findings and operator policy; this work applies that evidence to a
base-versus-head change rather than the accumulated backlog.

The difficulty is that blocking is adjudication, and this project's oldest invariant is that ABOM
does not adjudicate — see `.github/instructions/architecture.instructions.md`. The resolution is
that **the verdict is the operator's, computed by us**: ABOM reports which side of an advisory's
version range an observed reference falls on, with depth, reach and findings beside it, and a
declared policy says what each side means. A blocked pull request is explainable as *"your policy
said block on X, and here is X"*, never as *"ABOM thinks this is bad"*.

Two decisions follow from that and are fixed. The policy is a declared artifact the operator owns,
scoped to an organization, a business unit or a single repository, matching how ownership is already
declared; nothing is implied, defaulted to blocking, or inferred from severity. And the gate scopes
to the **delta** between base and head rather than the accumulated backlog, because a gate that
blocks on the backlog is switched off within a week and a gate nobody runs protects nothing.

## Pending

1. **Base-versus-head closure diff.** Distinct from the existing generation delta, which compares
   two states of one crawl rather than two commits. It yields nodes newly reachable, exposures
   newly in range, findings on newly reached nodes, and policy-refused actions newly reachable.
  Each side uses its own effective dependency selection from
  [actions-lockfile.md](../db/actions-lockfile.md), including changes caused only by a new lock.
  No current live tag or current default-branch lock can substitute for either revision.
2. **`GET /api/repositories/{owner}/{name}/verdict?sha=&base=`** — evidence, policy, outcome and
  reason in one response, with repository, both commits, governing lock contexts and resolution
  coverage echoed so every client can check which evidence was evaluated. Workflow callers use
  [org-server.md](org-server.md) authorization. Depends on 1.
3. **The check run**, so branch protection can require it and the outcome carries its explanation.
   Depends on 2.
4. **The client**, thin, which asks the API for a verdict at a SHA and renders it. It analyzes
   nothing, which makes it a third client of the same API as the interface and the CLI, per
   [`api-first-invariant.md`](api-first-invariant.md). Its own plan is
   [`abom-check.md`](abom-check.md), deferred and refined there until it is ready. Depends on 2.
    The planned client is verdict-only. Publication expansion stays deferred; native locking and
    updates do not become extra operations in the gate.

Open: whether an inconclusive check maps to a neutral conclusion or a failure. Neutral does not
block, which makes fail-closed unrepresentable through that mechanism alone. And a repository whose
account is declared but which the crawl has not reached is inconclusive by definition, which may be
the common case on first adoption.

## The constraints that must not be broken

- **The policy is the operator's; ABOM only evaluates it.** No rule may be implied, defaulted to
  blocking, or inferred from severity alone.
- **A rule may only be written in terms the evaluation actually applies.** A policy predicate is a
  saved view; silently dropping a term could make the gate refuse more than its author selected.
  [policy-language.md](policy-language.md) owns what a rule may say and what advisory or reachability
  terms assert. This plan enforces that vocabulary rather than inventing one of its own.
- **A verdict records both inputs**, evidence and policy together, or the block cannot be
  explained afterwards and will be disabled the first time it is disputed.
- **Inconclusive is never silently a pass.** A check that passes when it could not analyze the head
  commit manufactures confidence, which is worse than no check. Which way it falls is a stated
  choice in the policy, never a default nobody noticed.
- **The gate reads; it never crawls.** A check that triggers analysis makes pull request latency a
  function of the supply chain's depth.
- **A verdict never submits a snapshot.** A successful GitHub write is not a policy decision, and
  policy evaluation grants no permission to perform one.
- **A broken lock is not a live-tag fallback.** Unknown effective selection remains inconclusive.
  Missing native coverage uses a labeled declared-ref path only where the adopted policy permits
  it; lock presence alone supplies neither code safety nor an invented vulnerability verdict.

## Verification

- A blocked pull request's output names the policy rule and the evidence separately, so a reader
  who disagrees can tell which of the two they disagree with.
- A pull request that adds no dependency passes without consulting the backlog, and one whose
  dependency is already on the base branch does not block even when it matches the policy.
- A head SHA that has not been analyzed returns **inconclusive**, and the configured fail-closed
  and fail-open settings each produce the documented outcome — checked both ways.
- Verdict latency is measured. If it cannot be answered in seconds from stored state, the design is
  wrong rather than slow.
- Wrong-target and wrong-commit responses are rejected by clients. A verdict request cannot invoke
  submission or analysis, regardless of whether its caller separately has those permissions.
- A lock-only change produces the appropriate base/head comparison. Two roots locking the same
  ref differently cannot share effective results; unknown lock semantics cannot silently pass.
