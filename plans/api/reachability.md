# Step-level reachability

**Status:** exact, rendered, and condition-aware. CFG ingestion pending.

## Why this exists

This is the question package-version matching cannot answer, and the reason this project exists
alongside Dependabot. Not *do you depend on this*, but *does anything you run actually arrive at
this step, through whom, and guarded by what*.

## Done

`graph.Builder.ReachOfNode(nodeKey, maxDepth)` generalizes the traversal that already existed —
`exposureOf` walked dependents for any node; only `affectedNodes()` restricted it to advisory
targets.

Semantics, which took some care to get right:

- **hops 0** — the node lives in that repository; it does not depend on anything to run its own
  step. Reporting it at one hop would say it calls itself.
- **hops 1** — the repository names its container directly.
- A step is reached by whoever invokes its **container**, because nothing references a step.

`AbomStepConditions.ql` emits each `if:` verbatim. On the reference corpus: 202 repositories
reach `actions/checkout`, and 67 condition facts came from a three-repository run.

Endpoints: `/api/node/{key}/-/reach` and `/-/observations`. The node page renders the first as
"What actually reaches this": one row per repository, its route, and the guards on it.

## Conditions are joined to routes now

`ReachOfNode` carries the `if:` on each step the route passes through. The walk is the only
thing that knows *which* step made each reference, so the guards are gathered during it rather
than joined afterwards — a later join could only say that some step in the container is
conditional, which is a much weaker claim.

On the reference corpus: 23 repositories reach `aws-actions/configure-aws-credentials`, and
**5 of them only past an `if:`**. `pytorch/FBGEMM` arrives at 3 hops guarded by
`${{ inputs.trigger-event == 'schedule' || (inputs.trigger-event == 'push' && startsWith(github.event.ref, 'refs/heads/nightly')) }}`
in `pytorch/test-infra/.github/workflows/_binary_upload.yml`. "23 repositories configure AWS
credentials" and "18 do, and 5 more do on nightly builds" are different findings.

## What the corpus check falsified

Two things, neither predicted.

**The conditions were being recorded against a key that names nothing.** A composite action's
`runs:` block has no job, so the ingester read an empty `subjectJob` as "not a step" and fell
back to the workflow key — which, for `greet/action.yml`, is not even the composite action's own
key. Every step in one manifest therefore collapsed onto a single subject, where
`UNIQUE(query_run_id, subject_kind, subject_key, name)` kept exactly one of them. Position is
now checked before the job. These are the conditions that matter most: a composite action's
steps run inside every caller.

**Nothing was capturing them in the first place.** `AbomStepConditions.ql` is in
`abom-dependency-graph.qls`, but `serve` defaults `--expand-suite` to empty and runs the single
edges query, so a normal crawl produced no conditions at all. The 67 that existed came from a
three-repository run — and all 67 sat in *upstream* repositories' own CI, which by construction
is never seed-reachable, so not one of them could ever have appeared on a route. The join had
been correct and empty. Re-running the direct tier under the full suite took conditions to 620
(289 joined to a node) across 10 repositories, and the first guarded routes appeared
immediately. This is the "zero results is a coverage gap, not a broken traversal" rule paying
for itself.

## The approximation that remains

Reach reports each repository's **shortest** route, so a guard that exists only on a longer path
is not shown. `actions/cache` is reached by 77 repositories and reports 0 guarded, even though
`pytorch/test-infra/.github/actions/run-script-with-cache` guards it with
`${{ inputs.cache-path != '' }}` — because every one of those repositories also names
`actions/cache` directly. Shortest-route is the right default; "every route" is a different
question and would need its own endpoint.

## Pending

1. **CFG ingestion.** `actions/tools/print-cfg` is now usable (see below) and gives
   intra-workflow control flow. That is what turns "this step is reached" into "this step is
   reached, and here is what stands between the entry point and it".
2. **`StreamResultSet(name)`** in `internal/bqrs` for `nodes`/`edges`.
3. **Tier → suite binding**, so the graph suite is what a crawl runs by default rather than
   something an operator has to know to pass. See `plans/api/query-suites.md`.
4. **All-routes reach**, for guards that only exist on a longer path than the shortest.

## The line that must not be crossed

Conditions are reported **verbatim and never evaluated**. `github.ref == 'refs/heads/main'` is a
fact about the workflow; whether it holds on a given run is not something static analysis
settles. Reporting the expression lets a reader judge. Evaluating it would be this tool inventing
a verdict — the same rule that governs advisories.

Equally: reach is a route, not a verdict. Nothing in `ReachOfNode` judges whether arriving is
dangerous.

## Upstream dependency

`actions/PrintAST.ql` and `PrintCFG.ql` in `codeql-development-mcp-server` were fixed to select
files through the `selectedSourceFiles` extensible predicate, matching the other nine languages.
Before that, PrintAST returned nothing for any real repository and PrintCFG emitted every node in
the database. Both are verified against a real database: 56 nodes / 118 edges for one workflow.

Gotchas recorded there: the actions library exposes no `File` type; splitting the path match into
a helper predicate breaks binding analysis; an extension pack needs `library: true`; and
`codeql query run` needs both `--additional-packs` and `--model-packs`.
