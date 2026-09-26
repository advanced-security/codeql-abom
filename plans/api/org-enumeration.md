# Finding what changed without reading the whole organization

**Status:** built for account reads, workflow-aware database probes and visible freshness;
visual review and the missing-repository policy remain open.

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

1. **Complete the visual review of Settings.** The new listing column has API-grounded browser
   checks at 1440 and 390 CSS pixels, but the verification session exhausted its image budget.
   Review the full-listing, incremental-read and never-read states in both themes; do not treat
   passing DOM and overflow assertions as an appearance verdict.
2. **What to do about a repository that is gone.** A full listing now counts them and removes none:
   `missing` is a number, and `missingKnown` is false after a sweep because a prefix cannot
   establish absence. Whether such a repository is pruned, kept as coverage or labeled orphaned is
   the open decision, and it is the same one `next.md` records for seeded repositories in no
   declared account.

## The constraints that must not be broken

**A sweep is two walks, so it has a floor of two reads.** One ordered page costs 1 point; the
full-listing cost follows pages read, not the active repositories returned after archived rows
are excluded. An account that fits on one page therefore costs **2 points
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

- Review recorded, incremental-only and never-read states at desktop and mobile widths in both
   themes. Keep the API-versus-DOM checks, but do not substitute them for viewing the captures.
- A repository deleted after the last full listing is still reported until the next one, and the
   chosen removal policy must distinguish a full listing's absence from a sweep or a failed read.
