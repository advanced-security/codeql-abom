# Advisory enrichment

**Status:** shipped and verified against the real corpus (2026-09-05), with a known cost: the
refresh runs inside the cycle and blocks it for as long as it takes.

## Why this exists

`go list -deps ./cmd/codeql-abom` does not include `internal/advisory`. The package has 14
passing tests and **zero production callers**; `AddAdvisories` is called only from tests. The 20
advisories in the reference database were loaded by something that no longer exists, and
`advisoriesFetchedAt` has been frozen since.

The comment in `internal/serve/discovery.go` states it plainly: *"Nothing here fetches them."*

This is the fourth instance of the same pattern in this codebase — tested, correct, and
connected to nothing. `Rescore`, `LinkFindingsToNodes`, `BuildExposure` and `AbomSurface.ql` were
the others. Worth a standing check: a package with tests and no callers is not "done".

## What that costs

"Has advisories" cannot change as dependencies are analyzed. A dependency discovered today is
matched against an advisory set fetched before it existed, so the answer is stale by
construction and nothing on screen says so.

## Done

1. **`Supervisor.Advisor func(context.Context) (int, error)`**, with `refreshAdvisories` and
   `advisoryRefreshDue`. A failure is recorded through `fail` and the cycle continues —
   advisories enrich, so an outage must not stop dependencies being analyzed.
2. **`cmd/codeql-abom/advisories.go`** wires `advisory.Client.Fetch`, which already batches,
   paginates, deduplicates across batches and explains rate-limit exhaustion. Paced at 600/hour
   so enrichment cannot starve discovery of quota. Token from the environment
   (`GITHUB_TOKEN`/`GH_TOKEN` via the existing `githubToken()`), falling back to `gh auth token`,
   then unauthenticated at 60/hour.
3. ~~A store method listing packages worth querying~~ — **this was wrong when written.**
   `store.AdvisoryPackages()` already existed and already returned distinct `owner/repo` for
   external `action`, `composite_action`, `reusable_workflow` and `workflow` nodes. Nothing was
   needed here. Repository-scoped, because the API does not know about subpath actions: an
   advisory naming `github/codeql-action` covers `.../init` too.
4. **Conversion**, in `storeAdvisories`. The two types are field-identical today, which is
   exactly what makes a hand-written mapping dangerous: a field added to one side and forgotten
   compiles, stores and renders, and every advisory simply looks unscored forever.
   `TestConvertingAnAdvisoryCarriesEveryField` compares the whole struct rather than spot fields.
5. **An age ceiling and a floor**, `AdvisoryMaxAge` (24h) and `AdvisoryMinInterval` (15m), seeded
   from `store.AdvisoriesFetchedAt()` so a restart does not refetch a set collected minutes ago.
   Both were forced by the corpus check below, not designed up front.

## What the corpus check falsified

The original trigger was arrival alone: refresh after a cycle that ingested. Against the real
database that is **inert** — the frontier is fully drained (1213 done, 40 unavailable, nothing
pending), so no cycle ever ingests again and `advisoriesFetchedAt` would have stayed frozen at
`2026-09-04T05:49:28Z` forever. Advisories are published whether or not we analyzed anything
today; the answer ages on its own. Hence the ceiling.

The floor came from measuring the run: **936 packages, 19 paced requests, 109 seconds**, with the
cycle blocked throughout. On a live crawl ingesting every 30s cycle, the loop would have spent
almost all of its time re-asking about advisories instead of analyzing dependencies.

## Measured, on a copy of the real corpus

```text
advisories            20 -> 34
fetched_at            2026-09-04T05:49:28Z -> 2026-09-05T02:56:18Z
advisory packages     14 -> 28 named,  12 -> 23 that we actually have nodes for
exposure             253 rows / 13 GHSAs / 115 repos  (unchanged)
```

**Exposure not moving is correct, and worth recording so nobody "fixes" it.** All 14 new
advisories name actions whose `abom_blast_radius.dependent_repos` is 0 — `step-security/harden-runner`
has 1490 incoming edges and every one of them is from an `upstream` repository, never a `seed`.
Upstream repos contribute edges, not dependents. The transitive walk itself is fine: the 13
GHSAs that did land distribute across hops 1 (201), 2 (42) and 3 (10).

## Pending

- **Surface the fetch age** wherever advisory-derived numbers appear.
- **The refresh blocks the cycle.** 109 seconds is tolerable at a 15m floor and would not be if
  the package list grew an order of magnitude. Moving it off the cycle means a second writer, so
  it needs the single-writer constraint thought through rather than a goroutine.

## The invariant this must not break

Advisories enrich; they never adjudicate. A GHSA is stored as an attributed external statement,
exactly as a CodeQL result is attributed to a rule. Never derive "this action is vulnerable" from
an advisory alone — the observed reference may sit outside the vulnerable range. Report the
advisory, its range and the observed refs verbatim, side by side.

This is not theoretical: `actions/vulnerable-action` is exact-match only against a curated list
and found nothing across 204 real repositories, because most usage is floating tags no
exact-match model can resolve. The advisory layer covers that gap precisely by declining to
judge.

## Verification

- A cycle that settles new dependencies moves `advisoriesFetchedAt` and can move the
  "Has advisories" count.
- Rate-limit exhaustion surfaces its remedy rather than a bare 403.
- No new column anywhere records a verdict. `TestSchemaHasNoVerdictColumns` still passes.
- A retraction is carried, not filtered: `TestAWithdrawnAdvisoryIsStillRecorded`. Dropping it
  would leave nothing able to explain why a finding disappeared.
- An advisory with no EPSS stays unscored rather than becoming a probability of zero:
  `TestAnUnscoredAdvisoryStaysUnscored`.
