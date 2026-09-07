# A dependency row names a version, not a package

**Status:** shipped. Rows name a version, an empty reference says *default branch*, `Used by` is
backed by the edges and split by ownership, and the preview's "and N more" now leads to the whole
list on the node page.

**Every number below was measured against the wide `gh mrva` seed**, before the re-scope in
`db/owned-scope.md` and `db/discovery-boundary.md`. `actions/checkout@v4` at 285 repositories
counts 204 seed repositories nobody owns; once the root set is declared it will be a small
fraction of that. The numbers are kept because they are what was measured, not because they
describe the corpus after re-scoping.

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
5. ~~The full list of users.~~ **Shipped.** `NodeUsersOf(key, ref)` backs
   `GET /api/node/{key}/-/users?ref=`, and each row of *References in use* on the node page opens
   the repositories naming that version. Discovery's "and N more" links there with the reference
   in the query string, so the page opens on the row the reader came from.

### What the full list had to be careful about

**291 of the corpus's 5,787 name-and-version pairs** exceed the five-name preview, so that is how
many rows dead-ended. The widest is `actions/checkout@v4` at **285 repositories**.

Cross-checked against the same numbers computed independently in SQL, exact in all three cases:

| | API | SQL |
| --- | ---: | ---: |
| `actions/checkout@v4` | 285 total, 76 in scope | 285, 76 |
| `actions/upload-artifact@v4` | 98 total, 43 in scope | 98, 43 |
| `actions/setup-node@v3` | 74 total, 7 in scope | 74, 7 |

**The per-reference totals must not be added up.** `actions/checkout` has 65 distinct references
whose totals sum to **1,468**, against **1,019** distinct repositories using it at any reference.
One repository naming three versions is three rows and one repository, and a page that summed
them would report half again as many users as exist.

**Uses and repositories are also different numbers.** `gradle/actions` names `checkout@v4` **146**
times; that is one repository and 146 places to edit. It is also `upstream`, so those 146 are
nobody's work here — which is why every row carries its origin rather than the list being filtered
to seeds. Of the 285, **76 are in scope and 7 are the operator's**.

**A `break` out of a `rows` loop deadlocked the process.** The pool is `MaxOpenConns(1)`, so the
open `*sql.Rows` still held the only connection when the follow-up count ran. It did not fail — it
hung for the full ten-minute test timeout, and only on the truncation path, because every other
path drains the loop and auto-closes. Reading rows in their own function so `defer rows.Close()`
fires first is the fix.

## The constraint that must not be broken

**Report the reference as written, never a normalized or corrected one.** `ref_raw` for 178 edges
is literally `actions/checkout@v7`, a version upstream does not publish. That is evidence about
what the organization runs, not corruption, and the same class as `actions/download-artifact@v8`
which `git ls-remote` confirmed was real. Showing it verbatim beside a resolution that fails is
the *point*: an unresolvable tag is a finding the current grid cannot express.

## Verification

- No row in Discovery renders a dependency without a reference.
- A tag-based row and a SHA-pinned row for the same action are visibly different rows.
- An unresolved tag says so rather than showing an empty commit.
- The full user list agrees exactly with the same set counted independently in SQL.
- A list cut by the budget says so; one that was not does not claim to have been.
- Ownership is unanswered rather than zero until an account is verified.
- An upstream user is listed and marked, never silently dropped and never counted as work.
