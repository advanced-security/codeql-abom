# A query has an origin, and a finding remembers which query produced it

**Status:** Three kinds of query already run and nothing tells them apart. `abom_finding.provider`
records the SARIF tool driver, which is `CodeQL` for all of them.

## Why this exists

ABOM already runs upstream queries from `codeql/actions-queries` and one query of its own,
`AbomExperimentalMarker.ql`. A third kind is coming: queries the operator writes, which
[`ql/query-authoring.md`](../ql/query-authoring.md) is about. A reader has to be able to tell them
apart, and today they cannot.

The only available channel is the `abom/` prefix on a rule id — a **convention**, enforced by a
naming instruction rather than by the data. A query is not obliged to follow it, a published pack
somebody else wrote may use it, and nothing in the store records that the convention was ever
consulted. That is a fact about a string, offered as though it were provenance.

This matters more than tidiness in three places.

**A reader deciding whether to trust a result** needs to know whether a rule came from GitHub's
maintained set, from this project, or from a colleague last Tuesday. Those carry different weight
and the interface currently shows them identically.

**A policy** names rule ids. A rule id that silently changes origin — because a pack was swapped,
or an upstream query was renamed into the same id — changes what a gate refuses with nothing to
say so.

**[`api/query-simulation.md`](../api/query-simulation.md)** has to answer *what would this query do
across the organization*, which is only askable if a query is a thing the store knows about rather
than a row that appears when it happens to fire.

There is a second gap underneath. `abom_query_run.query_version` and `pack_version` are columns that
**are never written**, though the pack is in hand where a run is recorded. Until they are filled,
"what has not run since pack X at vY" can only be answered by re-resolving every suite, and a
catalog entry cannot say what version of itself last ran.

## Pending

1. **Write the versions that already have columns.** `query_version` and `pack_version` on
   `abom_query_run`, from the resolved plan that already carries them —
   `internal/expand/suite.go` builds `store.SuiteQuery` with the pack and digest per query.
   Depends on nothing, and is the smallest useful thing here.
2. **A catalog of queries, not a set of suites.** A suite is a *program over packs* whose contents
   are only knowable by asking `codeql resolve queries`, which is why it cannot be the unit. The
   catalog row is a query: id, pack, pack version, kind, the shape `ingest.ShapeOf` assigns it, and
   where its source lives. Populated from the same resolution that already runs before every round,
   so it describes what would run rather than what somebody typed. Depends on 1.
3. **Origin as a recorded fact.** `upstream`, `abom`, `user` — decided from the pack the query was
   resolved from, which is knowable at resolution time and is not a string convention. Recorded on
   the catalog row and carried onto the run, so a finding's origin survives the pack being upgraded
   or the query being renamed. Depends on 2.
4. **A `origin=` term in the filter language**, alongside the `mode=` term
   [`db/analysis-mode.md`](analysis-mode.md) adds, and enforced on the same three sides. Depends
   on 3.
5. **The interface differentiates, visibly.** Upstream, ABOM and operator-written queries are
   distinguishable wherever a finding or a rule id is shown, and the catalog is somewhere a reader
   can look at the whole set rather than inferring it from what fired. Depends on 4.

## What this takes over from other plans

The pack plan retired into this one and [`ql/pack-promotion.md`](../ql/pack-promotion.md): the
unwritten version columns are item 1 here, because the catalog is what needs them, and publishing to
GHCR is that plan's first item. [`api/query-suites.md`](../api/query-suites.md) holds *which suite an
analysis was run under*; a catalog keyed by query rather than by suite answers a superset of it, and
the two must not both grow a notion of query identity.

## The constraints that must not be broken

**Origin comes from the pack, never from the rule id.** A prefix is a naming convention with no
enforcement behind it. Deriving origin from a string would make a catalog that agrees with the
instruction file rather than with the code, and the first pack that breaks the convention would be
mislabelled with nothing to detect it.

**A query is routed by its own metadata, never by its filename.** Carried unchanged from
[`api/query-suites.md`](../api/query-suites.md), and it applies to the catalog too: a renamed query
must not change its origin, its shape or how its results are read.

**A catalog entry is not permission to run.** What runs is still the tier binding, and later the
operator's selection. A catalog that silently became the run set would make installing a pack a way
of scheduling work.

**A query that nobody wrote Go code for still contributes.** The acceptance test for the whole
mechanism is that a throwaway query added to a suite lands its results, attributed, with no Go
change. A catalog must not become the thing that has to be edited first.

**An unrecognized result shape is refused loudly, never guessed.** A catalog row for a query whose
shape `ingest.ShapeOf` cannot assign is a row that says so, not a row with a default in it.

## Verification

- A round records a pack version for every query it ran, and "what has not run since pack X at vY"
  is answerable without re-resolving a suite.
- `codeql pack ls` finds every pack at the version in `.codeql-version`, and `codeql resolve queries`
  per suite returns the same counts across any move or rename: **a rename must not change what
  runs**, and must not change an origin either.
- The catalog and `codeql resolve queries` agree on the set, checked by comparing them rather than
  by trusting the catalog.
- A query moved between packs changes origin in the catalog and changes nothing about its existing
  findings, which were produced by the pack that produced them.
- `origin=upstream` and `origin=abom` return different sets on the crawled organization, and their
  union is every finding with an origin recorded.
- Mutation: renaming a query to remove an `abom/` prefix changes no origin anywhere.
