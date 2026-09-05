# Supply-chain classification

**Status:** exact. Classification is the seed-reachable closure, recomputed over the whole graph.

## Why this exists

A dependency reaches an organization two ways, and they are different claims:

- **runtime** — something under review executes it. A composite action's own steps run inside
  whoever calls the action; a reusable workflow's steps run for whoever invokes it.
- **build_time** — it only ever runs in an upstream project's own CI.

Both are followed, because compromising a release pipeline is how consumers receive a malicious
version. Reporting build-time work as though this organization ran it would overstate the
exposure, so they are stored and displayed apart.

## What this fixed

`enqueueDiscoveries` used to pursue a dependency only when **another repository** referenced the
container of the `uses:`. A composite action called by a reusable workflow *in the same
repository* failed that test, so everything it depended on was dropped.

On the reference corpus that was **415 of 988** external actions and reusable workflows never
queued at all — and only 46 of 573 frontier rows had ever been discovered transitively. The
recursion was barely running.

After the fix: frontier 573 → 987 rows, never-queued 415 → 1, split 720 runtime / 275 build-time.

## The mistake worth remembering

The first diagnosis was wrong, and confidently so. The measurement asked "does any *seed*
repository observe an edge to this?" — which is **definitionally false for every transitive
dependency at depth ≥ 2**. A handful of sampled CI workflows were generalized to all 415, and the
interface was given a sentence explaining why they were correctly out of scope.

Re-measured by the file the `uses:` actually lives in: 322 upstream-CI-only, **74 runtime-only,
19 both**. Ninety-three genuine runtime dependencies had been declared out of scope.

The lesson is about the oracle, not the code: check that the discriminator can distinguish the
thing you are asking about before trusting what it says.

## The classification, and how it is decided

Classification comes from `abom_edge.edge_kind`, which **CodeQL already emits** —
`composite-step-uses-action` for a step inside a `runs:` block, `step-uses-action` for one inside
a workflow job. Never re-derive this from file paths; that reinvents the parser and gets it
wrong.

A dependency is **runtime** when some edge naming it comes from a container in the
seed-reachable closure: every container in a seed repository, plus everything those containers
transitively call. `graph.ClassifyDependencies` computes it in one breadth-first pass. The same
closure in a recursive SQL CTE took **36 seconds** on the reference corpus, which is why it is
in Go, matching the reason the rest of `internal/graph` gives.

The repository behind a container is read from `abom_edge.repository_row_id`, never from the
node. A node upsert only ever revises `container_node_row_id`, so a container first seen as
somebody else's dependency keeps `repository_row_id = NULL`: **0 of 1290 external nodes carry
one**. Keying "is this in a repository under review" off the node would have silently classified
nothing.

## What the previous rule got wrong, in both directions

The local test — "is this container referenced by anything" — was described here as loose by one
hop. Measuring it said something worse: it was also **wrong in the base case**. A repository
under review runs its own CI workflow, and nothing references that workflow, so the local rule
classified `actions/checkout` as build-time. Recomputed over the stored graph it admitted only
537 runtime dependencies where the closure admits 578.

It never showed, because two other mechanisms hid it: `SeedFrontier` enqueues a seed
repository's dependencies with an empty supply chain, which `Enqueue` defaults to runtime, and
`Enqueue` may only ever *raise* a claim. The stored answer was right for the wrong reason, and
the rule underneath it was never exercised.

That stickiness also meant **a tightened rule could not reach an existing corpus at all**. 254
rows were runtime under a rule that no longer claimed it, and no amount of re-crawling would
have moved one of them. `frontier.Queue.Reclassify` is the only pass permitted to lower a claim;
it is sound precisely because its input is derived from the whole graph at once and so cannot
depend on the order repositories were analyzed in.

## Storage

`abom_frontier.supply_chain`, defaulting to `runtime`. Existing rows were back-filled as runtime,
which is correct rather than convenient: they were all admitted under a rule that required an
external reference.

Runtime wins on conflict inside `Enqueue`, so the order repositories happen to be analyzed in
cannot decide how a dependency is reported. `Reclassify` overrides that on a drained queue.

## Pending

1. **The remaining 2** never-queued dependencies are explained rather than fixed:
   `actionsdesk/lfs-warning` and `actionutils/sticky-comment` are named by repositories sitting
   at frontier depth 5, and `Enqueue` drops anything past `MaxDepth`. That is the depth bound
   working, not a gap in the rule. Raising the bound is the only thing that would change it.
2. **Propagate the distinction outward.** `abom_blast_radius.direct_dependents` still counts
   every edge into a node, including an upstream project's own CI, so that one figure mixes what
   the organization runs with what it does not. `dependent_repos` is already seed-only and does
   not have the problem.

## Verification

- `TestASeedRepositorysOwnWorkflowMakesItsDependenciesRuntime` pins the base case the local rule
  got backwards; `TestABuildTimeOnlyCallerDoesNotPropagateRuntime` and
  `TestACompositeActionNobodyUnderReviewCallsIsBuildTime` pin the two leaks;
  `TestACalledCompositeActionsOwnStepsAreRuntime` pins what must not regress.
- Real corpus, before and after: **runtime 832 → 578, build-time 421 → 675**, 254 demoted, 0
  promoted. Demotions sit at depth 2 (162), 3 (56), 4 (29) and 5 (7); **no depth-1 dependency was
  touched**, which is the base case holding.
- The check worth keeping: classification and `abom_blast_radius` are derived by different code
  from different tables, so they can be cross-examined. Afterwards all 675 build-time rows have
  `dependent_repos = 0` and all 578 runtime rows have `dependent_repos > 0`, with **zero
  disagreements**. Before, **254 rows claimed runtime while blast radius said no repository under
  review reached them** — the contradiction this work removed.
