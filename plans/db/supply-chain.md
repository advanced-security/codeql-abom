# Supply-chain classification

**Status:** shipped and load-bearing; the classification rule is an approximation.

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

## The classification, and its limit

Classification comes from `abom_edge.edge_kind`, which **CodeQL already emits** —
`composite-step-uses-action` for a step inside a `runs:` block, `step-uses-action` for one inside
a workflow job. Never re-derive this from file paths; that reinvents the parser and gets it
wrong.

The current runtime test is **local**: "the container is referenced by something". That is not
the same as reachable from a repository under review, so a build-time-only caller can propagate
`runtime` one hop. Making it exact means classifying against the seed-reachable closure — see
`plans/api/reachability.md`, which needs the same closure.

## Storage

`abom_frontier.supply_chain`, defaulting to `runtime`. Existing rows were back-filled as runtime,
which is correct rather than convenient: they were all admitted under a rule that required an
external reference.

Runtime wins on conflict, so the order repositories happen to be analyzed in cannot decide how a
dependency is reported.

## Pending

1. **Exact classification** over the seed-reachable closure.
2. **The remaining 1** never-queued dependency — trivial in count, but it should be explainable
   rather than rounded away.
3. **Propagate the distinction outward.** Blast radius and exposure do not yet distinguish
   runtime from build-time reach, so a build-time dependency currently contributes to figures
   that read as "what you run".

## Verification

- `TestExpandFollowsACompositeActionsOwnSteps` and `TestExpandLabelsUpstreamCIAsBuildTime` pin
  the two halves; `TestExpandUpgradesBuildTimeToRuntimeWhenSomethingCallsIt` pins the precedence.
- Real corpus, before and after, on: frontier size, never-queued count, runtime/build-time split.
