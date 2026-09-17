# Finding what changed without reading the whole organization

**Status:** built for the reads. GraphQL is the preferred route and REST the fallback, both answer
the same three questions, each account carries the marks a sorted walk stops at, and only a full
listing reports what is gone. The per-repository database probe is untouched and is now the ceiling.

## Why this exists

Reading everything, every period, is affordable at five hundred repositories and not at twenty
thousand. A full listing is one request per hundred; conditional requests make an *unchanged*
listing nearly free, but one insertion shifts every page after it and full price resumes there.

**Three questions, three costs.** Separated because a design that answers only the cheap two looks
current and quietly rots.

| Question | How | Cost |
| --- | --- | --- |
| What is new? | Order by creation, descending; stop below the newest known | The new prefix |
| What changed? | Order by push, descending; stop below the last sweep | The moved prefix |
| What is gone? | **A full listing. Nothing else.** | Every page |

`pushedAt` is the signal and `updatedAt` is a trap. The plausible reading — that `updatedAt` is
broader and so safer — is false: **17 of 27 repositories on the reference organization carry a
`pushedAt` later than their `updatedAt`**, which cannot happen if one contained the other.

## Pending

1. **The per-repository database probe is the remaining cost.** There is no code-scanning or CodeQL
   field on `Repository` in the GraphQL schema — checked by introspection, not supposed — so asking
   whether a repository has an actions database is REST and is one request each. At twenty thousand
   repositories that is the whole bill now that the listing is bounded.
2. **Narrow that fan-out before spending it.** Establishing which repositories hold a workflow file
   would spend the probe only where it can find something, and must report the ones it skipped as
   unprobed rather than as holding nothing. Depends on 1 being the thing worth attacking, which the
   measurement above says it is.
3. **What the interface says.** The API carries it — `/api/owned-scope` reports the route, when the
   account was last listed in full, when last swept, and the marks — and no screen reads it yet. A
   repository deleted after the last full listing is still reported until the next one, which is
   only defensible if a reader can see how long ago that was.
4. **What to do about a repository that is gone.** A full listing now counts them and removes none:
   `missing` is a number, and `missingKnown` is false after a sweep because a prefix cannot
   establish absence. Whether such a repository is pruned, kept as coverage or labeled orphaned is
   the open decision, and it is the same one `next.md` records for seeded repositories in no
   declared account.

## The constraints that must not be broken

**A sweep is two walks, so it has a floor of two reads.** One ordered page costs 1 point; a full
listing of N repositories costs ⌈N/100⌉. An account that fits on one page therefore costs **2 points
swept against 1 listed**, and gives up seeing deletions to do it; the saving begins at three pages.
`enumerate.sweepWorthIt` encodes that, and an account at or below two pages is read whole. Making
the incremental path unconditional would cost more and answer less.

**A sorted prefix cannot report a deletion.** A repository that no longer exists appears in no
listing, so no early-stopping sweep can mention it. Only a full listing establishes absence, and a
design that quietly stops doing them keeps dead repositories while looking current. **Never
enumerated is not enumerated and empty**: an account with no listing has an unknown shape, not zero.

**Neither route may be the only route.** A capability that exists on one route only makes an
organization unreadable rather than slower. The two must agree — same repositories, same marks — or
the route becomes a hidden variable in every figure downstream. They do not agree about everything:
REST reports a `default_branch` for a repository with no branches, because it is a setting, where
GraphQL answers `defaultBranchRef: null`. `abom_enumeration_state.route` is what makes a figure
attributable to the route that produced it.

**Pagination ordered by activity is unstable.** A push landing mid-pagination reorders the pages
still to come, so a sweep may see one repository twice and miss another. It tolerates both; the
periodic full listing is what repairs the miss.

**A GraphQL query is bounded, and the bounds bite quietly.** `first` and `last` are capped at 100, a
call may not request more than 500,000 nodes, and a query GitHub cannot answer in ten seconds is
terminated **and charges extra points against the next hour**. Secondary limits cap GraphQL at 2,000
points a minute, and the hundred-concurrent ceiling is shared, so separate budgets are not separate
clients.

## Verification

- **The cost model holds at size.** 1 point per hundred repositories was measured on 27, and a
  formula that divides by a hundred behaves differently either side of the rounding. Re-measure
  where the organization is large, and re-check `sweepWorthIt` against it.
- Requests and points spent by an incremental sweep on an organization of more than two pages, with
  nothing changed, one repository pushed and one created. Unbounded by what moved, it is a full
  listing wearing a sort order.
- A repository deleted after the last full listing is still reported until the next one, and the
  interface says when that was, so the staleness is visible rather than inferred.
