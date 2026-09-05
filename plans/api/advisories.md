# Advisory enrichment

**Status:** not wired in. `internal/advisory` is not in the binary at all.

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

## Pending

1. **An `Advisor` hook on the supervisor**, refreshing advisories for the packages that settled
   in each cycle. Keyed on the cycle rather than a timer, so the enrichment tracks the crawl.
2. **Wire the client**: `advisory.Client.Fetch(ctx, packages)` already batches, paginates,
   deduplicates across batches, and explains rate-limit exhaustion. Token from `gh auth token`,
   falling back to env, then unauthenticated at 60/hour.
3. **A store method listing packages worth querying** — distinct `owner/repo` for external
   actions and reusable workflows. Repository-scoped, because the API does not know about
   subpath actions: an advisory naming `github/codeql-action` covers `.../init` too.
4. **Conversion.** `advisory.Advisory` and `store.Advisory` are field-identical; the mapping is
   mechanical but must not be assumed to stay that way.
5. **Surface the fetch age** wherever advisory-derived numbers appear.

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
