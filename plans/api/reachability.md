# Step-level reachability

**Status:** reach is exact, condition-aware and rendered, and reports each repository's shortest
route only.

## Why this exists

This is the question package-version matching cannot answer, and the reason this project exists
alongside Dependabot. Not *do you depend on this*, but *does anything you run actually arrive at
this step, through whom, and guarded by what*.

Two parts of that answer are still missing. A guard that exists only on a longer route is not
shown: **77 repositories reach `actions/cache` and none are reported guarded**, even though
`pytorch/test-infra/.github/actions/run-script-with-cache` guards it with
`${{ inputs.cache-path != '' }}`, because every one of those repositories also names
`actions/cache` directly. And ordering is recorded but never presented, so "what stands between the
entry point and this step" is in the database and not on screen.

## Pending

1. **`StreamResultSet(name)`** in `internal/bqrs` for the `nodes`/`edges` result sets. Shared with
   `api/query-suites.md`.
2. **All-routes reach**, for guards that exist only on a longer route than the shortest.
   Shortest-route stays the default; every-route is a different question and needs its own
   endpoint.
3. **Render job ordering.** `job-needs-job` edges are recorded and deliberately excluded from every
   dependency aggregate — `needs:` is ordering, not use, and counting it would inflate reach with
   jobs from the reader's own workflows. No endpoint or view presents them.

**Deferred by decision: the CFG is not the source of ordering.** `actions/tools/print-cfg` builds
its graph from an AST pre-order walk, never consults `needs:`, and chains jobs in document order,
so for two independent jobs it asserts a sequence that does not exist — Actions runs jobs in
parallel unless `needs:` says otherwise. Its nodes are expressions rather than steps
(`github.repository`, `secrets.GITHUB_TOKEN`, raw `run:` bodies), with edges jumping backwards to
`env:` declarations. It is a taint-analysis graph, which is what it is for, and not a route through
a workflow. It also lives in another repository, needs an extension pack and `--additional-packs`,
and runs one file at a time, so nothing about it is reachable from a crawl.

## The constraint that must not be broken

**Conditions are reported verbatim and never evaluated.** `github.ref == 'refs/heads/main'` is a
fact about the workflow; whether it holds on a given run is not something static analysis settles.
Reporting the expression lets a reader judge. Evaluating it would be this tool inventing a verdict,
which is the rule that governs advisories too.

**Reach is a route, not a verdict.** Nothing in `ReachOfNode` judges whether arriving is dangerous.

**A step is reached by whoever invokes its container**, because nothing references a step; and a
node in its own repository is at **hops 0**, because reporting it at one hop would say it calls
itself.

## Verification

- A repository reached only past an `if:` is reported as reached *and* guarded, with the expression
  shown. "23 repositories configure AWS credentials" and "18 do, and 5 more do on nightly builds"
  are different findings.
- All-routes reach surfaces at least one guard the shortest route omits, `actions/cache` being the
  known case.
- Rendering `job-needs-job` moves no dependency aggregate: every level in `abom_blast_radius` and
  `abom_node_reach` stays as it was.
