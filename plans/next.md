# What to build next

An index over `plans/**`. Each plan holds the reasoning; this file holds the order and the
one-line reason for it. Re-rank freely — the ranking is a judgement, not a record.

Keep it terse. If an entry needs a paragraph, the paragraph belongs in its plan.

## Ranked next

1. **[Wire advisories in](api/advisories.md)** — `internal/advisory` is not in the binary at all.
   14 passing tests, zero callers, and `advisoriesFetchedAt` frozen since whatever loaded the
   current 20 stopped existing. Small, self-contained, immediately visible.
2. **[Exact supply-chain classification](db/supply-chain.md)** — the runtime/build-time test is
   local ("something calls this"), so a build-time-only caller propagates `runtime` one hop.
   Shares its seed-reachable closure with 3, so do them together.
3. **[Reachability: CFG and exactness](api/reachability.md)** — the differentiator. Reach is
   computed and served but nothing renders it, and conditions are captured but not joined to it.
4. **[Tier → suite binding as config](api/query-suites.md)** — `--expand-suite` applies one suite
   to everything. Until this exists, the tier controls in 5 have nothing to bind to.
5. **[Per-tier suite controls](ui/tier-suites.md)** — depends on 4.
6. **[Component catalog spike](ui/testing.md)** — BlazingStory, our own, or neither. Timeboxed,
   with criteria written before the code. Its own README says it may be abandoned; that is the
   decision, not a footnote.
7. **[Explorer component coverage](ui/testing.md)** — needs `JSInterop.SetupModule`. Selection
   state has two independent halves, so it is a phase rather than a few tests.
8. **[Observation store hardening](db/observations.md)** — indexes, retention, the two-of-59
   subject keys that did not join. Wait until read patterns are known; guessing indexes now
   would be guessing.

## Implemented

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
