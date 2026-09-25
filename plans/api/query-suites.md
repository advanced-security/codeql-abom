# Query suites as configuration

**Status:** tiers bind to suites as stored configuration, editable while the server runs, and both
the BQRS and findings lanes run from the same resolved plan. **Query identity, version and source
belong to the catalog**, which [`docs/architecture.md`](../../docs/architecture.md#what-produces-the-data)
describes — a suite is a program over packs, so it is the wrong unit to hang either on, and the two
must not both grow a notion of what a query is.

## Why this exists

CodeQL is a reprogrammable but deterministic inspection engine: the same query at the same version
against the same database gives the same answer. That makes the query set the natural place for
this project to grow, and it means the set cannot be compiled in. A query added next year must run
without a release.

What is configurable now is which suites a tier runs. What is still not expressible is which suite
a given analysis *was* run under, so "re-process with an updated suite" cannot be asked and
staleness can only be read per tier. Nothing renders the binding either, so editing it means
calling the API by hand.

## Pending

1. **`analyzed_suite` and `analyzed_suite_version` per frontier row**, so staleness is visible per
   suite rather than per tier and re-processing under a new suite is expressible. The version comes
   from the catalog rather than being derived here.
2. **Skip what has not changed.** `QueryRunExists` makes this cheap — only queries or databases
   that actually changed need to run again — and nothing consumes it.
3. **`StreamResultSet(name)`** in `internal/bqrs`, a sibling to `StreamSelect`. Every named result
   set already parses, so `nodes`/`edges` graph shapes are readable once the streaming entry point
   exists. Shared with `api/reachability.md`.
4. **`ingest.FindingsFromDirectory` has no caller** outside its own tests, and the package-level
   guard cannot see it because `ingest` itself is reachable. Give it a caller or delete it — it is
   a public ingest entry point, so removing it is a decision about what the ingest API offers
   rather than a tidy-up.
5. **A control for the binding.** `ui/tier-suites.md` — the endpoints exist and nothing renders
   them.

## The constraints that must not be broken

**A query is routed by its own metadata, never by its filename.** A renamed query must not change
how its results are read.

**An unrecognized result shape is refused loudly, never guessed.** A silently mis-ingested result
is worse than one that never arrives. The same holds for a binding that names findings queries with
no analyzer configured: refuse before the round starts rather than absorb it quietly.

**Bound to nothing is an instruction, not an absence.** A tier deliberately bound to `[]` must
survive a restart, which is why a separate record of whether a tier was decided at all sits beside
the binding. This is the store's absent-is-not-zero rule arriving in configuration.

**A package no command reaches is not implemented.**
`TestEveryInternalPackageIsReachableFromABinary` compares `go list ./internal/...` against
`go list -deps <module>/cmd/...` and fails naming anything no command reaches. Reading dead code
answers "does this work" and never "does this run", and no build, test run or review diff tells the
two apart.

## Verification

The acceptance test is for the mechanism, not the queries: **add a throwaway query to a suite and
confirm its results land, attributed, with no Go change.** If that ever requires code, the design
has failed. `TestAQueryNobodyWroteCodeForCanStillContributeFacts` guards the ingest half.
