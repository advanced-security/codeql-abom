# Step-level reachability

**Status:** shipped with a known approximation; CFG ingestion pending.

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

Endpoints: `/api/node/{key}/-/reach` and `/-/observations`.

## The approximation, stated plainly

The runtime/build-time test is **local** — "something calls this container" — not transitive
reachability from a repository under review. It is enough to stop composite actions being
dropped, but a build-time-only caller can propagate `runtime` one hop.

Tightening it means computing reachability from the seed set over runtime edges and classifying
against that closure, rather than asking a one-hop question. The machinery exists; wiring it is
the work.

## Pending

1. **Exact runtime classification** over the seed-reachable closure, replacing the local test.
2. **CFG ingestion.** `actions/tools/print-cfg` is now usable (see below) and gives intra-workflow
   control flow. That is what turns "this step is reached" into "this step is reached, and here
   is what stands between the entry point and it".
3. **`StreamResultSet(name)`** in `internal/bqrs` for `nodes`/`edges`.
4. **Surface reach in the interface.** The endpoint exists and nothing renders it.
5. **Condition-aware reporting**: a step reached unconditionally is a different claim from one
   behind `if: github.ref == 'refs/heads/main'`, and the interface should say which.

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
