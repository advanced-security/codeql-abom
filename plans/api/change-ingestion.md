# Learn about commits as they happen, within budgets the operator sets

**Status:** change is noticed only by the crawl re-analyzing what it claims and by a 24-hour recheck
sweep that one dependency failing every attempt holds off indefinitely; nothing receives an event,
nothing polls on a budget of its own, and no budget is visible.

## Why this exists

The graph changes only when the crawl reaches something again, and the recheck that returns a moved
reference to the queue is a sweep with a 24-hour floor: a workflow edited now is noticed up to a day
later, after probing every repository that did not change. That was tolerable for one organization
of a thousand repositories. ABOM's target is now an **enterprise — many organizations, up to 100,000
repositories each** — where pushes arrive all day and a sweep cannot keep up at any affordable rate.

**And the sweep can stop without saying so.** It probes only once every depth is complete, and a
transient failure returns to pending however often it recurs, so one dependency that can never
succeed holds the recheck off for good. On the crawled organization two owned repositories do:
`has-ghas/codeql-clone` has a branch yet to be born, and `has-ghas/ghas-bootcamp`'s database build
exits with status 32, eight attempts each. Depth 0 was incomplete in all **113 recorded cycles**,
and none of them probed a reference. The drained-queue sweep for unqueued discoveries and the
reclassification wait on the same rows, because any pending row means the queue is not drained.

The two halves of the problem have different shapes. **An owned repository can tell ABOM it
changed**: an enterprise App installed on its organization receives the event. **An external
dependency cannot**: ABOM has no App on somebody else's repository, so a moving tag or branch is
found only by asking. On the crawled organization **1,214 of 2,562 resolved references are pinned to
a commit and never need asking; 1,348 name something that can move** — 984 tags or branches, and 364
default branches.

And every request comes out of a shared budget. An enterprise's GitHub rate limits are already
contested, so ABOM can never assume its App gets all of them. **The operator sets how fast ABOM may
ask, per kind of work, and ABOM says what freshness that buys** — never a promised time limit.

## Decisions

- **Owned repositories are read on events from an enterprise App**, through the identity and address
  in [`api/enterprise-server.md`](enterprise-server.md). A push touching `.github/workflows/**`,
  `action.yml` or `action.yaml` can change the graph; any other push moves the live commit to one
  whose declarations are identical, settled without a clone as
  [`db/commit-aware-graph.md`](../db/commit-aware-graph.md) describes. Branch creation, deletion and
  protection changes move what is live; repository lifecycle and installation events move what is
  owned.
- **External dependencies are polled**, ordered by the constrained reach of the version that would
  move and by how mutable its reference is. A commit pin is never polled. Requests are batched where
  GraphQL allows and conditional where REST does.
- **GitHub's dependency graph is a hint for owned pushes.** Where it is enabled, the dependency-review
  comparison between a push's base and head says cheaply whether direct Actions dependencies moved,
  which orders that push's analysis ahead of others. It never skips an analysis and never writes an
  edge: only the crawl changes the graph's shape, and an independent reader is not ground truth.
- **One token pool per process.** Enumeration, owned-repository event follow-up, external reference
  polling, fetching, advisory reads, lock verification, analysis capacity, the
  [consistency checker](../tools/graph-consistency-checker.md) and each Copilot-backed agent each
  draw from a pool whose rate and burst the operator sets in the interface. A pool is denominated in
  what its provider charges — REST calls, GraphQL points, git requests, worker-seconds, Copilot
  credits — and GitHub's own limits are a ceiling over all of them, read from every response, never
  assumed to be ABOM's alone.

## Pending

1. **A dependency that fails every attempt must not hold the sweep off.** Either a repository with
   no commit, or with nothing the extractor reads, is unavailable — a fact about the repository, as
   the crawl already treats a database with no source, though this build exited 32 without the
   message that check reads — or completeness counts a row past an attempt ceiling as settled for
   now, and says so on the depth chip — a policy about the crawl, which covers failures nobody has
   classified yet. The same rows make Settings say *Completing depth 0, and nothing deeper is
   started until it is done* while deeper work runs, because a row waiting on a retry is not
   claimable. Nothing depends on it, and every item below falls back to the sweep it unblocks.
2. **Token pools with meters.** Persisted per-pool configuration, enforcement at the one place each
   process asks, and accounting in a table of its own: `abom_observation` is CodeQL output about
   subjects, never what a cycle spent. Each pool's meter shows spend per provider against its limit
   and GitHub's reported remaining, and a database that has never spent reports no spend rather than
   zero.
3. **A freshness estimate per operation.** From a pool's rate and the work queued behind it — how
   long until every moving reference of a given reach has been asked about again — shown beside the
   meter, and checked against the lag the consistency checker measures. An estimate that never meets
   a measurement is not one. Depends on 2.
4. **Poll external references under their pool.** Ordered by constrained reach and mutability,
   batched, conditional, and settling a moved reference through the existing requeue, which decides
   what happens. Depends on 2. The reach of the version that would move is
   `abom_node_version_reach` at the commit its reference resolves to now.
5. **Receive the enterprise App's events.** Verify `X-Hub-Signature-256` before a payload is parsed,
   append it to a durable log before acknowledging, and process the log in order; a receiver that
   loses an event on restart is a sweep with extra steps. Depends on the App and the address in
   [`api/enterprise-server.md`](enterprise-server.md).
6. **`security_advisory` as a signal.** A new advisory changes exposure with no repository changing,
   which no poll and no push can notice.
7. **The claim order as an objective with named stages.** Unanalyzed inside the closure at the
   current depth; the shallowest unexplored depth; then the stalest live version. Expedited work
   stays ahead of all three, because an operator naming one repository states an objective the crawl
   cannot infer. Re-observing a commit whose declarations are not known already runs before every
   claim, up to one cycle's claims at a time, and becomes part of the first stage.
8. **Fall back to the sweep, never replace it.** A webhook removed, ingress down or a repository
   nobody installed on degrades to the recheck, late, rather than to silence. The sweep becomes a
   pool like any other, so its cost is visible too. Depends on 1.

## The constraints that must not be broken

**An event is a hint, not a fact.** It says something may have changed; the probe still decides. A
payload's SHA is never trusted in place of resolving the reference, or a replayed or forged event
rewrites what the corpus believes it read.

**Spend is what the provider charged.** GraphQL's `remaining` moves with everybody's traffic; read
`rateLimit { cost }`, and a figure subtracted from a shared counter is not this pool's spend.

**An unknown budget is not a budget of nothing, and a pool with room leaves the work alone.** A limit
nobody reported must not throttle, an exhausted one must not read as unlimited, and pacing guards
against exhaustion rather than governing.

**`checked_at` means probed, and nothing else writes it.** While it was set together with
`analyzed_at`, "probed after analysis" was structurally impossible, and the signal saying freshness
works read as though it always did.

**A freshness estimate is derived, never promised.** It follows from the rate the operator chose and
the work in the queue, and it says so; showing a time as though it were a commitment is the coverage
lie in a different unit.

## Verification

- A push touching a workflow is re-analyzed after one probe of one repository; a push touching
  nothing the graph reads moves the live commit without a clone.
- Each meter agrees with what the provider charged over the same interval, on the crawled
  organization, within the provider's own reporting precision.
- With events disabled, the sweep finds the same drift, late, and its pool shows what it cost.
- The freshness estimate and the lag the consistency checker measures agree, per pool, or the
  difference is named.
- Efficiency at enterprise scale is measured on real organizations as they become available; until
  one is, figures here are stated for the organization they came from and nothing larger.
