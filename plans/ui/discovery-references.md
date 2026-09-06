# A dependency row names a version, not a package

**Status:** shipped. Rows name a version, an empty reference says *default branch*, and `Used by`
is backed by the edges and split by ownership. One item is deliberately left over: the preview
stops at five names, and the full list belongs on the node page.

## What it measured

| | |
| --- | --- |
| `actions/checkout@v4` named by | **220 repositories** |
| ...of which the operator owns | **7** |
| ...of which are `seed` | 75 |
| rows whose preview held the junk `/` repository | **27 → 0** |

**Ownership, not seed, is the split that matters** — and the numbers say why. Seed would have
reported 75, still ten times the number the operator can act on. `UsedByOwned` reuses the
`abom_account` predicate behind `isOwned`, so ownership cannot drift from what the owned tier
means, and "0 yours" is never printed: with no verified account that is an unanswered question,
not a count of none.

**What reconciling corrected, before any code changed.** The C# `FrontierItem` had **no `Ref`
property at all** — the Go struct and the JSON carried it and the model dropped it silently, so
the discard point was one layer earlier than this plan claimed. The "Analyzed commit" column
already rendered `ResolvedSha`, so item 3 largely existed. `.abom-ref` already existed as a
styling class.

**What the corpus check found that unit tests could not.** The junk `/` repository — an artifact
of the settle regression — appeared in the used-by preview of 27 rows. The query was right; the
data was wrong. Cleaning it required deleting the full dependency closure, because foreign keys
refused the naive delete: `abom_finding_node`, `abom_alert_link` and edges referencing those nodes
as *endpoints* rather than by repository all held references. Verified afterwards with
`PRAGMA foreign_key_check` empty and `PRAGMA integrity_check` ok.

## Why this exists

Fourteen consecutive rows read `actions/checkout`, score `2186`, tier `direct`, differing only in
the trailing "named by". They are not duplicates. They are fourteen different versions, and the
one column that would say so is not drawn.

The store already holds the distinction — the queue was re-keyed `(node_key, ref)` and every row
carries its own `ref`, `resolved_sha`, state and coverage. `FrontierItem.Ref` reaches the API.
The grid renders `NodeKey` alone, so all of that arrives and is thrown away at the last step.

**There is no case for showing an action's name without its reference.** A name is a package; a
reference is what runs. Findings, advisories and pinning all attach to the second.

## What is already true

Measured on the corpus (`/tmp/abom-real/abom.db`), mid-crawl:

| | |
| --- | --- |
| frontier rows | **2,344** |
| `actions/checkout` rows | **31**, at 31 distinct refs |
| ...distinct `resolved_sha` among them | **1** |
| refs that are empty (default branch) | **1,253** |
| refs that are a tag or branch | **745** |
| refs that are already a commit SHA | **346** |
| rows with `resolved_sha` populated | **1,213** of 2,344 |

## Two corrections the naive design needs

**1. The aggregation key cannot be the resolved SHA alone.** 1,131 rows have no `resolved_sha`,
because it is written when a row is analyzed and most rows are still queued. Grouping on it would
fold every unanalyzed version into one bucket labelled with nothing — an absent measurement drawn
as a measurement, which is the invariant this repository already has three scars from.

Group on **`resolved_sha` when it is known, otherwise the ref as written**, and let the row say
which of the two it is. A SHA-pinned row needs no second identity: 346 rows already name a commit,
so for those the as-written ref *is* the resolved one.

**2. "Used by" cannot come from the frontier.** `discovered_from` holds a single first-discoverer,
which is what the current "named by" column prints. The real set of users lives on `abom_edge`:
`actions/checkout@v4` is named by **220** repositories, `@v7` by 178, one SHA-pinned version by
165. A collapsed list needs a new scoped read over edges, not a wider frontier row.

## Pending

1. ~~Render `name@ref` everywhere a dependency is named.~~ **Shipped** for the Discovery grid.
2. ~~Decide what an empty ref renders as.~~ **Shipped**: *(default branch)*, because 1,253 of
   2,352 rows have one and a blank would re-create the bug for half the queue.
3. ~~Show the tag and its resolution together.~~ Largely already existed — the "Analyzed commit"
   column renders `ResolvedSha` beside the reference.
4. ~~Rename `Note` to `Used by`.~~ **Shipped**, edge-backed and split by ownership.
5. **Still open: the full list of users.** The preview stops at five and says "and 213 more",
   which is honest and a dead end. The node page is where every repository using a version
   belongs — a different page and a different query shape, so it is its own entry.

## The constraint that must not be broken

**Report the reference as written, never a normalized or corrected one.** `ref_raw` for 178 edges
is literally `actions/checkout@v7`, a version upstream does not publish. That is evidence about
what the organization runs, not corruption, and the same class as `actions/download-artifact@v8`
which `git ls-remote` confirmed was real. Showing it verbatim beside a resolution that fails is
the *point*: an unresolvable tag is a finding the current grid cannot express.

## Verification

- No row in Discovery renders a dependency without a reference.
- The 31 `actions/checkout` rows read as 31 versions, and their "Used by" counts sum to the edge
  count for that node.
- A tag-based row and a SHA-pinned row for the same action are visibly different rows.
- An unresolved tag says so rather than showing an empty commit.
