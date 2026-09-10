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

## What measuring changed about the design

**A sweep is two walks, so it has a floor of two reads.** One ordered page costs 1 point, measured
directly; a full listing of N repositories costs ⌈N/100⌉. So sweeping an account that fits on one
page costs **2 points against the listing's 1**, and gives up seeing deletions to do it. The saving
only begins at three pages. `enumerate.sweepWorthIt` encodes that, and an account at or below two
pages is read whole — which is why the reference organization is fully listed on every cycle and
still answers what is gone. The incremental path is for the twenty-thousand repository case it was
designed for, and the small case now takes the cheaper road rather than the cleverer one.

**REST names a default branch that does not exist.** `has-ghas/codeql-clone` has zero branches and
REST reports `main`, because `default_branch` is a repository *setting*. GraphQL answers
`defaultBranchRef: null`, which is the honest answer, and the preferred route is the one that does
not invent the branch. Telling the two apart over REST costs a request per repository, which is the
fan-out this design exists to avoid, so the difference is stated rather than papered over — and
`abom_enumeration_state.route` is what makes it attributable.

**GraphQL's own `remaining` is not an instrument.** Three consecutive probes each reported `cost=1`
while `remaining` did not move. Spend is measured from `rateLimit { cost }`, never from a delta
between two readings of what is left.

## GraphQL is the preference; REST is the fallback that must keep working

One call returning a hundred repositories with `name`, `pushedAt`, `createdAt`, `isArchived` and the
default branch costs **1 point**, ordering is a first-class argument, and the budget is **separate
from REST's**. Adding `repositoryCustomPropertyValues` keeps it at 1, which is where
[`../db/business-units.md`](../db/business-units.md) takes its scoping dimension from.

REST's `/orgs/{org}/properties/values` returns every repository's values a hundred at a time, so
properties do *not* cost a request each and both routes are roughly two hundred calls. What survives
is the separate budget; one walk that arrives already correlated, where two REST walks join on a
name a repository can change between them; and every further fact being another field rather than
another endpoint.

`shurcooL/githubv4` is the usual answer and is not: **no tagged release**, and three dependencies
added to a module of three whose purpose is measuring dependency risk, to avoid a JSON `POST`.

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

**A sorted prefix cannot report a deletion.** A repository that no longer exists appears in no
listing, so no early-stopping sweep can mention it. Only a full listing establishes absence, and a
design that quietly stops doing them keeps dead repositories while looking current. **Never
enumerated is not enumerated and empty**: an account with no listing has an unknown shape, not zero.

**Neither route may be the only route.** A capability that exists on one route only makes an
organization unreadable rather than slower. The two must agree — same repositories, same marks — or
the route becomes a hidden variable in every figure downstream.

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
