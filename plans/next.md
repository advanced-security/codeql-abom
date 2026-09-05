# What to build next

An index over `plans/**`. Each plan holds the reasoning; this file holds the order and the
one-line reason for it. Re-rank freely — the ranking is a judgement, not a record.

Keep it terse. If an entry needs a paragraph, the paragraph belongs in its plan.

## Ranked next

1. **[Explorer legibility](ui/performance.md)** — the view is now fast (3.4 → 60 frames per
   second, 419 → 68 MB) and still not readable: 603 owner groups is a smaller hairball, and
   folding by owner makes every edge cross-owner so the edge encoding goes flat. Bounded scope is
   the half not yet done, and it is now a legibility problem rather than a performance one.
2. **[Tier → suite binding as config](api/query-suites.md)** — `--expand-suite` applies one suite
   to everything, and defaults to empty, so a normal crawl runs only the edges query and captures
   no step conditions at all. Until this exists the tier controls in 3 have nothing to bind to.
3. **[Per-tier suite controls](ui/tier-suites.md)** — depends on 2.
4. **[CFG ingestion](api/reachability.md)** — turns "this step is reached" into "and here is what
   stands between the entry point and it". `actions/tools/print-cfg` is usable now.
5. **[Component catalog spike](ui/testing.md)** — BlazingStory, our own, or neither. Timeboxed,
   with criteria written before the code. Its own README says it may be abandoned; that is the
   decision, not a footnote.
6. **[Explorer component coverage](ui/testing.md)** — needs `JSInterop.SetupModule`. Selection
   state has two independent halves, so it is a phase rather than a few tests.
7. **[Observation store hardening](db/observations.md)** — indexes, retention, the subject keys
   that did not join. Wait until read patterns are known; guessing indexes now would be guessing.

## Implemented

- **An Explorer that can be used** — the simulation now holds only what is visible, the view
  arrives folded by owner, and a settled layout with a still camera stops drawing altogether.
  3.4 → 60.7 frames per second, 9,811 → 603 simulated nodes, 419 → 68 MB, and a heap that falls
  back after a filter instead of climbing to 643 MB. Three things the measurements killed:
  removing the simulation gate changed the default view by nothing, because with no filter every
  node is visible and the default was a scope problem all along; quiescence driven by the
  controls' `change` event never fired, because damping keeps that event coming for ever;
  and the heap was never a leak, it just tracked the simulation's size. `ui/performance.md`
- **Exact supply-chain classification** — the local "is this container referenced" test replaced
  by the seed-reachable closure. Runtime 832 → 578, build-time 421 → 675; 254 demoted, 0 promoted,
  none at depth 1. The old rule was not merely loose by one hop: it called `actions/checkout`
  build-time, and only `SeedFrontier`'s default plus `Enqueue`'s one-way stickiness hid it. Those
  same mechanics meant a tightened rule could never reach an existing corpus, so `Reclassify` is
  the one pass allowed to lower a claim. Cross-checked against `abom_blast_radius`, derived
  separately: afterwards zero disagreements, where before **254 rows claimed runtime while blast
  radius said no repository under review reached them**. `db/supply-chain.md`
- **Reach rendered, and conditions joined to it** — `/-/reach` now carries the `if:` guarding each
  step on a route, and the node page renders route and guard together. 5 of 23 repositories reach
  `aws-actions/configure-aws-credentials` only on nightly builds. Two things the corpus check
  caught: composite-action step conditions were keyed to a workflow path that names no node and
  collapsed to one row per file, and nothing captured conditions on a normal crawl at all — all
  67 that existed sat in upstream CI, which is never seed-reachable, so the join was correct and
  necessarily empty. Under the full suite: 67 → 620 conditions, 25 → 289 joined.
  `api/reachability.md`
- **`/implement-abom-plan`** — one command from "what next" to a measured before/after, with the
  two steps that get skipped by hand (reconcile first, diff the corpus last) made explicit.
  Unproven until it has driven an entry end to end. `tools/implement-plan-skill.md`
- **Advisory enrichment** — `internal/advisory` went from zero production callers to reachable
  from the binary. Advisories 20 → 34, `fetchedAt` unfrozen after a day, covered packages 12 → 23.
  The corpus check killed the original design: arrival alone is inert on a drained queue, and the
  refresh costs 109s for 936 packages, so it gained a 24h ceiling and a 15m floor.
  `api/advisories.md`
- **Transitive traversal** — classification from `abom_edge.edge_kind`, plus a drain-time sweep.
  415 never-queued dependencies → 1; frontier 573 → 987. `db/supply-chain.md`
- **Extensibility spine** — `abom_observation`, metadata-driven routing, four result shapes,
  `QueryRunExists`. A new query contributes facts with no Go. `db/observations.md`
- **Suite-driven execution** — `codeql resolve queries`, one round per resolve, findings left to
  their own lane. `api/query-suites.md`
- **Step reachability** — `ReachOfNode`, hops 0/1/n semantics, `/-/reach` and `/-/observations`.
  `api/reachability.md`
- **`AbomStepConditions.ql`** — `if:` verbatim, never evaluated. `api/reachability.md`
- **`AbomSurface.ql` resurrected** — was referenced by nothing but its own test; now the first
  customer of the observation path. `db/observations.md`
- **Tier re-analysis** — `RequeueTier`, guarded like Start, cost stated on the control.
  `ui/tier-suites.md`
- **bUnit component tests** — 10 tests in `make verify` and CI, each named after a shipped
  regression. `ui/testing.md`
- **Upstream `actions` PrintAST/PrintCFG fix** — `selectedSourceFiles`, matching the other nine
  languages. Previously returned nothing for any real repository. `api/reachability.md`

## Not doing, and why

- **Evaluating `if:` conditions.** Whether `github.ref == 'refs/heads/main'` holds on a given run
  is not something static analysis settles. Report the expression; let a reader judge.
- **Deriving vulnerability from an advisory.** The observed reference may sit outside the
  vulnerable range. Report the advisory, its range and the refs side by side.
- **A `pin_status` column, or any verdict column.** `TestSchemaHasNoVerdictColumns` guards this.
- **Rewriting the Explorer's renderer, or splitting the server into services, to fix Explorer
  performance.** Both were considered and neither is supported by the measurements: the renderer
  holds 60 FPS at 430 nodes, and every server endpoint answers in under 550 ms. A service
  boundary drawn through code that is already fast moves the same 8 MB across one more hop. If
  either earns its place it will be on its own evidence — see `ui/performance.md`.
