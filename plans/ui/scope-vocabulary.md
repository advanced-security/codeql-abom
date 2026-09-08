# One vocabulary for ownership, and one for boundaries

**Status:** planned. Found by the interface audit on 2026-09-07.

## Why this exists

ABOM held four notions of ownership and the interface conflated them. **One of the four is now
retired**, which is most of this plan.

**"Under review" was never a real thing and is being removed, not renamed.** `repository.origin`
records how a repository entered the corpus — this one arrived in a `gh mrva` database, that one
was fetched to resolve a dependency. That is provenance of the data, not a statement about anyone.
Calling the first group "under review" or "in scope" made it read as a claim of responsibility,
and it is not one: **nothing about ABOM happens until an operator claims ownership of an account
or a repository.** There is no state in which a repository is under review by nobody.

So three notions survive, and only one of them means responsibility:

- **Yours** — `abom_owned_scope`. Declared. The only one that implies you can change anything.
- **A member of** — `abom_account`. What `gh` reports, which decides nothing.
- **Reached** — whatever the declared scope depends on, at any depth.

`origin` stays in the store, where it is a useful record of how a row got there, and stops being
rendered as an ownership claim anywhere.

### What this leaves wrong today

The persistent strip says **"204 repositories in scope"** on every page, drawn from
`summary.seedRepos`. The declared scope is **1 repository across 2 accounts** — and was **44
across 2 accounts** when this was audited, because the corpus then declared `facebook` and
`microsoft`, which the operator has never owned (`db/declaration-provenance.md`). Correcting the
declaration made the contradiction **worse, not better**: 204 against 1. The figure is stable and
the declaration is not, which is the whole reason one must not be printed as the other.

The Overview goes further: *"Every GitHub Actions dependency reached by the 204
repositories in scope — those in the organization that were analyzed with CodeQL."* There is no
"the organization"; that corpus spans facebook, microsoft, google and spring-projects, none of
which the operator owned until they said so. The landing page still tells the pre-re-architecture
story, and it is the first thing anyone reads.

The Explorer says it outright in its own legend: *"in scope and upstream are not colored apart —
use the Origin filter."*

**Boundaries.** `db/edge-relationships.md` gave every edge a stored class. Three surfaces name it
three ways:

| Surface | Words |
| --- | --- |
| Explorer legend | Same repo, Same org/owner, Cross org/owner |
| Bill of Materials filter | Same repository, Same account, Another account |
| Store, API, exports | `same-repository`, `same-owner`, `cross-owner` |

A reader who filters "Another account" on one page and "Cross org/owner" on another has no way to
know they asked the same question.

## What is already true

The retired wording is not in one place. Reconciling found it across nine files:

| Where | What it says |
| --- | --- |
| `Shared/CoverageStrip.razor` | "repositories in scope", on every page |
| `Pages/Repositories.razor` | page title "Repositories in scope"; "those in the organization" |
| `Pages/Discovery.razor` | "the repositories under review"; "a repository under review names this itself"; "Referenced by something in scope" |
| `Pages/Settings.razor` | "Accounts holding repositories in scope"; column "Repositories in scope" |
| `Pages/Remediation.razor` | "No repository in scope reaches an action…" |
| `Pages/Inventory.razor` | `"seed" => "in scope"` |
| `Shared/PinningLevel.razor` | "across everything in scope" |
| `Models/Models.cs` | "One repository under review…"; "`seed` for a repository under review" |
| `tests/…/NodeReachTests.cs` | asserts the retired string |

| | |
| --- | --- |
| declared scope | reachable only at `/settings` and `/api/owned-scope` |
| Explorer | states that scope and upstream are not distinguished |
| boundary words | three vocabularies, none of them wrong, none of them shared |
| `abom-owned-mark` | defined and rendering, used on one page |

## The design

**One word per notion, chosen once and used everywhere.**

- *Yours* — `abom_owned_scope`. Declared, and the only one that implies you can change it.
- *Reached* — what the declared scope depends on, at any depth.
- *A member of* — `abom_account`. What `gh` reports, which decides nothing.

The strip states the declared scope first, because that is the number every other figure is scoped
by, and keeps the corpus figure beside it as **coverage** rather than as scope: "1,099
repositories analyzed" is a fact about how much has been looked at, and stating it does not claim
anyone owns them. Both halves, always, which is the rule the rest of the interface already follows
for analyzed and unanalyzed.

For boundaries, the reader-facing words follow the Bill of Materials, which is the surface where a
non-expert reads them: **same repository / same account / another account**. The stored values do
not change; `boundaryClass()` already maps them and gains no new responsibility.

### What must not happen

The corpus figure must not disappear. A corpus of 1,099 repositories of which an operator has
declared 44 is worth stating; hiding it would make coverage unreadable and would be the same
mistake in the other direction.

## Phases

1. The vocabulary, written down, including which notion each of the strings above means today.
2. The strip: declared scope, and corpus coverage beside it, neither called "in scope".
3. The Overview standfirst, which currently asserts a single organization.
4. The remaining seven files, and the test that asserts the retired string.
5. Boundary words unified across the Explorer legend and the Bill of Materials.

## Verification

- `grep -rn "under review\|in scope" ui/ --include=*.razor --include=*.cs` returns nothing outside
  the store's own `origin` documentation.
- The declared count on the strip matches `/api/owned-scope` on a corpus where it differs from
  `seedRepos` — the audited corpus, where they were 44 and 204.
- One boundary vocabulary: the three retired spellings appear nowhere in the interface.
- The existing style pass and `misspell` stay clean; "repositorys" appears in the Explorer's
  collapse note today and is fixed here.

## Open questions

- **What the Repositories page becomes.** Its title, its filter and its standfirst all rest on the
  retired notion. Listing every repository with an *owned* / *reached* mark is the obvious
  replacement, but it is a different page from the one that exists.
