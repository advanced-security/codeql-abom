# What to build next

An index over `plans/**`. Each plan holds the reasoning; this file holds the order and the
one-line reason for it. Push and pop freely. Re-rank as needed — the ranking is itself a plan,
and plans are always subject to change over time.

Keep it terse. If an entry needs a paragraph, the paragraph belongs in its plan.

## Ranked next

1. **[Select and pin](db/analysis-ledger.md)** — selection predicates as filters and controls, so
   "re-analyze what has not run since pack X vY" is a query rather than a whole-tier requeue;
   `pinned_at` ahead of `score` in `Claim`.
2. **[Warm JVMs via `codeql execute`](db/analysis-ledger.md)** — `cli-server` for the ~38
   `resolve metadata` calls a round plus every decode, `query-server2` for the BQRS lane. Each
   currently pays 2-5s of JVM start.
3. **[Theme and layout audit](ui/theme.md)** — the palette is now checked as arithmetic; the
   MudBlazor semantic colours and every layout question still are not, and layout is where the
   known defects are.
4. **[CFG ingestion](api/reachability.md)** — turns "this step is reached" into "and here is what
   stands between the entry point and it". `actions/tools/print-cfg` is usable now.
5. **[Give `internal/archive` a command or delete it](api/query-suites.md)** — a working, tested
   pipeline no binary reaches. It is why the findings lane looked implemented for as long as it
   did. Cheap, and it removes a standing source of the same mistake.
6. **[Overlay databases](db/analysis-ledger.md)** — they map onto the recheck loop directly, but
   the flags are Wizards-only. Probe and degrade. After the ledger, which decides what to run at
   all.
7. **[Run the CodeQL update workflow for real](ql/packs.md)** — `update-codeql.yml` and
   `bump-pack-versions.sh` are exercised locally and have never run in Actions.
8. **[Component catalog spike](ui/testing.md)** — BlazingStory, our own, or neither. Timeboxed,
   with criteria written before the code. Its own README says it may be abandoned; that is the
   decision, not a footnote.
9. **[Explorer component coverage](ui/testing.md)** — needs `JSInterop.SetupModule`. Selection
   state has two independent halves, so it is a phase rather than a few tests.
10. **[Observation store hardening](db/observations.md)** — indexes, retention, the subject keys
    that did not join. Wait until read patterns are known; guessing indexes now would be guessing.
11. **[Publish packs to GHCR](ql/packs.md)** — deliberately deferred until much later. The naming
    and versioning landed early so that publishing is never the thing blocked.

## Implemented

- **Every finding says what it is, independently of the run** — `normalize.ResultHash` over the
  rule, the repository-relative path and CodeQL's `primaryLocationLineHash`, stored on
  `abom_finding.result_hash`; and `tool.driver.semanticVersion` checked against the `cli_version`
  the ledger recorded, so SARIF from a different analyzer fails ingestion rather than being
  attributed to the wrong one. The plan's own "only real test of verifiable" passes on the real
  corpus: one repository analyzed, its ledger rows and findings dropped so the skip could not
  fire, analyzed again — **15 findings, byte-identical hashes across both rebuilds**, and **zero**
  version mismatches. Grouping across the corpus is **115 groups over 307 rows, widest 10
  repositories**; the plan had guessed twelve. Reconciling also caught the chain contradicting
  itself — it specified `result = H(analysis, normalized result)`, and an analysis hash contains
  the repository, so that hash could never have been shared across repositories at all. The third
  part of the entry, `NOT NULL` identity columns, is **not done and should not be**: 1,555 of
  1,571 ledger rows still predate the identity. `db/analysis-ledger.md`

- **Work already on record is not done twice** — `expand.alreadyRecorded` asks `QueryRunExists`
  for every row `tierPlan.runs` would write, and a repository with all of them present is settled
  without a database build, a query or an analysis. Four repositories re-analyzed immediately
  after analysis: **109 s → 28 s wall clock, 20 → 0 CodeQL invocations, 16 → 0 ledger rows**, with
  nodes, edges and findings unchanged. The skip sits after the checkout because the run key
  includes the commit, and deciding from `abom_frontier.resolved_sha` would skip precisely when a
  ref had moved. It travels as `Prepared.AlreadyRecorded` rather than `ErrSkip` because only
  `Consume` completes items, so a skip returned as an error strands its batch forever. Two callers
  outside the expansion read "nothing analyzed" as "nothing left to do": `expand-all` would have
  ended its round loop on the first fully skipped round, and the supervisor would have logged
  nothing for the duration of a re-analysis, which is indistinguishable from a hung crawl.
  `db/analysis-ledger.md`

- **Every run says what produced it** — `abom_query_run` carries `pack`, `pack_version`,
  `pack_digest` and `cli_version`, all inside `run_key`; `runner.PackDigest` hashes the pack
  directory and `DigestCache` keeps one hash per pack per process, since the 35-query findings
  suite would otherwise re-walk `actions-queries` once per repository. On the real corpus the
  three graph queries record `abom-actions-graph-extraction-src@2.26.4`, the findings lane records
  `codeql/actions-queries@0.6.34`, and every row carries `cli_version 2.26.4`. Two defects that
  unit tests could not see: `pack_digest` was added to `CREATE TABLE` but not to `addedColumns`,
  so a real database — upgraded in place, being hours of crawling — failed at the first write
  while every fresh-database test passed; and `plansByTier` merged suites by appending the two
  query slices only, dropping the round's CodeQL version on the way into the tier, which showed
  up as a NULL `cli_version` beside a `pack` and digest that looked right, because those travel
  inside `plannedQuery`. `db/analysis-ledger.md`

- **One CodeQL invocation at a time** — `runner.Lock` around `database create`, `database
  analyze` and `query run`: a mutex for this process's workers and an advisory `flock` for a
  second abom process, cancellable so pausing discovery need not wait out a database build.
  Measured at **1 concurrent invocation during a `--concurrency 4` crawl**, against 4 by
  construction before. The measurement was wrong twice first, which is why the corrected sampler
  is checked in: `pgrep -f` matches the sampling shell's own argv, and one invocation is a chain
  of three processes — the `gh` shim, the `codeql` wrapper, the JVM — all carrying the same argv.
  Counting lines reports 3 for a single run. `db/analysis-ledger.md`
- **One Discovery control box, measured** — run controls, re-analyze and the suite binding in one
  box; a suite line carrying its pack version and query count, expanding to the query ids behind
  its own disclosure. At a 720px viewport the box is 150px at 1600 wide and the queue starts at
  441 with 7 rows visible, against 517 for the old two-panel layout; at 1280 the re-analyze
  buttons wrap and it is a wash. Consolidating alone *cost* height — 24px to a 1px divider's
  margins and 18px to uppercase labels wrapping — which is why the entry existed. And
  `white-space: nowrap` made it worse before it made it better: the row overflowed instead of
  wrapping, clipping "Re-analyze direct (42", a truncated number on the control whose job is
  stating cost. Nine component tests stayed green through it. `ui/discovery-config.md`
- **Packs are the versioned unit** — `ql/` restructured to mirror
  `server/ql/<lang>/<purpose>/{src,test}`: `abom-actions-graph-extraction-{src,test}`,
  `abom-actions-findings-src`, `abom-actions-selfcheck-src`, all scoped under
  `advanced-security` and all at `2.26.4`, matching `.codeql-version`. Two of the four own no
  queries, which is the point: their version tracks *which upstream queries ABOM selected*.
  Named `graph-extraction` rather than `extraction`, which in CodeQL already means turning source
  into a database — the opposite end of the pipeline from these queries.
  Suites are now `<pack>:codeql-suites/<name>.qls` everywhere, an identity that survives both the
  directory move and publication, and `AvailableSuites` reads pack manifests rather than one
  directory. Stored bindings are rewritten by a migration that runs before seeding, so a
  deliberate choice is not overwritten and an unrecognized binding is kept verbatim.
  `codeql resolve queries` returns 3 / 35 / 28 — unchanged by the move, which is the whole test
  of a rename. `ql/packs.md`
- **`make check-versions`** — exact pinning writes the same upstream version into a suite, a pack
  manifest and a lock file, and `codeql pack upgrade` rewrites two of the three, never the one
  inside a `.qls`. Proved by injecting a wrong pin rather than by reading the code — which is how
  the first version was caught running its loop behind a pipe, in a subshell, reporting every
  mismatch and still exiting 0. `ql/packs.md`
- **Discovery configuration folds; what it is set to does not** — the suite control went from
  304px to 62px and the frontier table from 666px down a 720px viewport to 517, so the queue is
  readable without scrolling. The header keeps stating the binding — `direct:
  abom-dependency-graph, abom-findings · transitive: abom-dependency-graph` — because a fold that
  hid it would leave a reader assuming the default. Two departures from the plan: the
  expansion-panel trap it warned about was *avoided* rather than handled, since rendering the body
  under a plain `@if` makes a folded grid genuinely absent and therefore assertable; and nine
  green component tests said nothing about the header rendering centered, which one screenshot
  caught at once. `ui/discovery-config.md`
- **One dev server per machine, enforced by process** — `start`, `stop` and `status` all find the
  server the same way, by executable name plus a `serve` argument, instead of trusting a pid file
  that only remembers what the Makefile itself launched. A server started by hand used to be
  invisible to all three: `start` reported success because the *old* server answered its readiness
  probe, `restart` was a no-op, and `status` denied it existed — which is how an afternoon of
  changes stayed invisible behind a binary seven hours old. The guard is the process, not the
  address, because a second server on another port is still a second writer against the database.
- **The tier binding control says why, where the choice is made** — the rationale moved out of a
  paragraph above the grid, which named neither tier, into the column headers: each tier now
  carries its name, why it differs from the other, and the work a tick would apply to. A read that
  *failed* is no longer drawn as a read still arriving — an indeterminate bar spanning the panel
  read as a pulsing border and waited for ever against a server too old to have the endpoint.
  `ui/tier-suites.md`
- **Findings lane for upstream repositories** — a crawl now runs what its tier is bound to in both
  lanes: findings-shaped queries through `database analyze`, everything else through `query run`,
  from one resolved plan. The reconciliation was the whole story — `AnalyzeDatabase`,
  `FindingsFromSARIF` and `internal/archive` wiring them together all existed and were tested; the
  gap was one `continue` in `expand.plan`, and `internal/archive` turns out to be unreachable from
  the binary. Two things were wrong on the way: the findings run was given an invented query id
  when every result already carries its rule, and the edges run was named from `--query-id`, which
  is empty once a crawl runs from a binding. A binding with no analyzer is now refused rather than
  absorbed. On the reference corpus a single round took findings 4,531 → 4,620: 89 findings across
  18 upstream repositories under 4 rules, none of which a crawl had ever produced before.
  `api/query-suites.md`
- **Per-tier suite controls** — a grid on the Discovery page, one row per suite and one column per
  tier, each column headed by the work a change would apply to (direct 380, transitive 691 on the
  reference corpus). Suites are read from disk per request, so one appears by being dropped in;
  anything already bound stays offered even when it is not on disk, because a binding may name a
  pack's suite. The corpus check argued *against* the obvious tidy-up: filtering out
  `abom-selfcheck.qls` by name would reintroduce exactly the release-coupled hard-coding the
  control exists to remove. `ui/tier-suites.md`
- **Tier → suite binding as configuration** — `abom_tier_suite`, seeded at migration and
  rewritable at run time; a batch is planned by the shallowest depth it holds and a shared suite
  resolves once per round. On the reference corpus: direct binds graph + findings, transitive
  binds graph alone, and `PUT` edits either without a restart. The design needed a second table
  it did not start with — "bound to nothing" is a real instruction and was indistinguishable from
  "never configured", so every restart would have seeded the defaults back over it.
  `api/query-suites.md`
- **A scoped Explorer that is legible** — the default is `scope=exposure`: only the routes by
  which repositories under review reach an action an advisory names. 277 nodes and 486 edges,
  14 KB on the wire against 473 KB, 45 MB of heap against 419, at 60.7 frames per second, and
  44 ms of server time against 331. Folding bought frames; scope bought meaning — at 277 unfolded
  nodes an edge can be followed end to end, which 603 owner groups never allowed.
  `ui/performance.md`
- **A palette checked as numbers** — "Queued" was the same string as `Muted`, the colour for a
  node that was never a dependency, at 2.69:1 on dark and 2.07:1 on light against a 3:1 floor, so
  a queue of 422 read as a disabled control. Auditing the whole palette found three more: `org`
  and, worse, **`Focus` — the selection colour — at 2.48:1 in light mode**, so selecting a node
  barely changed it. `PaletteContrastTests` now computes WCAG contrast for every entry in both
  schemes, so this cannot regress by eye. `ui/theme.md`
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
