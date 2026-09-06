# Step-level reachability

**Status:** exact, rendered, condition-aware, and ordered. Job ordering from `needs:` is ingested;
the CFG approach that preceded it is refused, with evidence, below.

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

1. **`StreamResultSet(name)`** in `internal/bqrs` for `nodes`/`edges`.
2. **All-routes reach**, for guards that only exist on a longer path than the shortest.
3. **Nothing reads the ordering yet.** `job-needs-job` is recorded and deliberately excluded from
   every dependency aggregate; no endpoint or view presents it. Ingesting it first is the right
   order — the edges accumulate as repositories are re-analyzed — but until something renders it,
   the answer to "what stands between the entry point and this step" is in the database and not
   on screen.

## What the ordering measured

On 54 re-analyzed repositories: **331 `job-needs-job` edges**, taking the corpus from 35,457 to
35,803 edges and 54,509 to 54,527 nodes — the extra nodes are jobs that no `uses:` ever names,
which exist only because something needs them.

**The aggregates did not move, and that is the whole point.** `needs:` is ordering, not use, so a
traversal that counted it would inflate reach with jobs from the reader's own workflows:

| | before | after |
| --- | --- | --- |
| blast radius rows | 2,194 | 2,194 |
| `SUM(dependent_repos)` | 3,127 | 3,127 |
| `SUM(direct_dependents)` | 35,457 | **35,472** |
| jobs holding a blast radius | 0 | 0 |
| runtime / build-time | 578 / 675 | 578 / 675 |

The one number that moved is the proof. `SUM(direct_dependents)` equalled the edge count exactly
before, because every edge contributes one direct dependent. Afterwards the corpus holds 35,803
edges and the sum is 35,472 — **35,803 minus exactly the 331 ordering edges**, with the +15
accounted for by genuinely new `step-uses-action` rows the re-analysis picked up.

**"Unchanged" nearly went in the record as a result when it was an absent measurement.** The
first reading showed blast radius identical and was about to be reported as the filter working —
but `abom_graph_generation` was still at generation 99 with the old edge watermark, which is the
repository's own rule that a measurement nobody took is not a measurement of zero. What settled
it was that the rows had in fact been rewritten, visible only in the sum.

## Why not the CFG

This plan carried "CFG ingestion" as its top pending item for a long time on the strength of
`actions/tools/print-cfg` becoming usable. Reconciling it against the library and a real database
killed it, and the reasons are worth keeping so it is not proposed again.

**It is not ABOM's query.** `actions/tools/print-cfg` lives in `codeql-development-mcp-server`,
needs the `selectedSourceFiles` extensible predicate, an extension pack marked `library: true`,
and `--additional-packs` with `--model-packs`, and it runs one file at a time. Nothing about it
is reachable from a crawl as it stands.

**It is an AST walk, not control flow.** `codeql/actions-all@0.6.0` builds the graph from
`StandardPreOrderTree` over AST children, with scopes limited to `Workflow` and `CompositeAction`.
On a real workflow the entire graph is a chain:

```text
enter → name: 'ShellCheck' → Job: shellcheck → Uses Step → Uses Step → exit
```

**It never consults `needs:`.** `grep needs Cfg.qll` returns nothing; jobs are chained by
`getAJob()` in document order. Where the chain happens to agree with the dependency graph it is a
coincidence of file layout — in `record.yml` and `test.yml` the chained pair *does* declare
`needs:`, and the CFG would have chained them identically had it not. **For two independent jobs
it therefore asserts a sequence that does not exist**, because Actions runs jobs in parallel
unless `needs:` says otherwise. Ingesting that as "what stands between" would be this tool
inventing an ordering, which is the same line the condition and advisory rules already draw.

**Its nodes are expressions, not steps.** The graph includes `github.repository`,
`secrets.GITHUB_TOKEN`, `env.TAG_NAME` and raw `run:` script bodies, with edges that jump
backwards to `env:` declarations — `Uses Step` at line 23 to an expression at line 12. Useful for
taint analysis, which is what it is for; not a route through a workflow.

So the truthful parts of "what stands between" are already held (`step_index`, conditions) or are
`needs:`, which the CFG cannot supply.

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
