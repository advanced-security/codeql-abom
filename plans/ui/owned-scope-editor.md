# Declaring what you own, from the interface

**Status:** shipped, and its verification numbers describe somebody else's supply chain — see
*What the corpus said*. The Settings page declares, undeclares, enables, restricts and previews,
and no longer needs a terminal. Phase 6 ships as an **Open Discovery** control; the enumeration
that would make it a shorter wait is `ui/first-run.md`.

## Why this exists

`db/owned-scope.md` made the operator's declaration the thing the whole architecture turns on:
discovery is bounded by it, pruning is measured against it, and the bill of materials is rooted in
it. **The only way to make that declaration is the command line.**

The Settings page renders the declaration and cannot change it. Its entire control set is one
`CHECK WITH GH` button and the data grid's own sort and pagination — audited directly, there is no
add, no remove, no enable toggle, no include-list editor. `PUT /api/owned-scope` exists and is
control-guarded; `AbomApi` has `GetOwnedScopeAsync` and no setter.

So a first-run operator opens ABOM, reads "Which GitHub accounts are yours is something you state,
not something we infer", and finds nothing on the page that lets them state it. Every downstream
page then reports on a scope of zero, or on whatever a previous `codeql-abom own` invocation left
behind.

## What is already true

| | |
| --- | --- |
| `GET /api/owned-scope` | serves the declaration |
| `PUT /api/owned-scope` | accepts a whole replacement, control-guarded like `discovery/start` |
| `store.SetOwnedScope` | wholesale replace, `includes_only` stored rather than derived |
| `AbomApi` | read only |
| Settings page | read only |
| `codeql-abom own` | `--org`, `--user`, `--include`, `--clear` |

The store already refuses an include for an undeclared account, and already distinguishes
"restricted to these repositories" from "every repository" as a stored flag rather than an
inference from an empty list — so removing the last include cannot silently widen an account. The
interface has to preserve that distinction rather than reintroduce it.

## The design

The "Accounts you own" table becomes editable, and the "Accounts you belong to" table below it
becomes the source it draws from — promoting a membership to a declaration is the common case and
should be one click, not a form.

- **Declare** from the membership list, and from a free-text field for an account `gh` does not
  report, because ownership is stated rather than inferred and the interface must not restrict the
  statement to what a token happens to see.
- **Undeclare**, with what it will cost stated first: the repositories that leave the closure and
  the queue rows that stop being worked. Undeclaring is how a corpus silently shrinks.
- **Restrict to named repositories**, holding `includes_only` explicitly, with the empty-list case
  refused rather than treated as "all".
- **Enable and disable** without undeclaring, so a temporarily-out-of-scope account keeps its
  include list.

A save replaces the whole declaration, matching `SetOwnedScope`, so the page must show the
resulting scope before it is written rather than sending a diff nobody assembled.

### What must not happen

`gh` verification stays advisory. The audit found the two declared accounts marked
*not confirmed by gh* in amber, correctly — that is the design working, and the editor must not
turn it into a validation error that blocks declaring an account.

### A declaration is not the end of the journey

Saving one leaves the operator on a page that has just changed everything downstream and says
nothing about it. The editor states what happens next and how to start it — the repositories being
enumerated, and then Start on Discovery. `ui/first-run.md` builds the phases behind that sentence;
this plan owns the sentence.

## Phases

1. `AbomApi.SetOwnedScopeAsync`, and the control-header handling the other write endpoints use.
2. Declare and undeclare from the membership list.
3. The free-text declaration for an account `gh` does not report.
4. Include lists and the enable toggle.
5. The consequence preview: what enters and leaves the closure, before the write.
6. What to press next, once there is something to press.

## Verification

- A first-run operator with an empty declaration can reach a working bill of materials without
  touching a terminal.
- Declaring an account with no repositories in the corpus is accepted and reported as reaching
  nothing, rather than silently succeeding and leaving every page empty.
- Removing the last include does not widen the account to every repository; `includes_only`
  survives the round trip.
- The control guard refuses a non-loopback write, matching `discovery/start`.
- An account `gh` does not report can still be declared, and stays marked unconfirmed.
- After a save, the page names the next action, and following it produces a visible change.

## What the corpus said

**Every number in this section was measured against a declaration that was false.** The corpus
declared `facebook` and `microsoft`; `gh` reports 33 accounts for this operator and neither is
among them. So *44 roots*, *486 closure* and *2,292 queued* are arithmetically correct
descriptions of Facebook's and Microsoft's supply chain. They verify the **mechanism** and assert
nothing true about ownership. The operator caught it by reading their own Settings page; the
interface audit of the same day had recorded "2 declared accounts ✓" as a passing cross-check.
The honest declaration — `data-douser` and `advanced-security` — gives **1 root, a closure of 270
and 1,642 queued rows**. See `db/declaration-provenance.md`.

Kept as measured, because a plan that quietly restates its numbers teaches nothing about how they
got there.

Measured against a copy of the reference corpus with `facebook` and `microsoft` declared.

**The preview agrees with the store exactly.** `rootsNow` 44, `closureNow` 486 and `queuedNow`
2,292, each matching the same quantity computed independently in SQL — the roots predicate
longhand, and the closure as a recursive CTE over `abom_edge_live` with the same
`DependencyEdgeFilter`. Undeclaring everything reports **486 repositories and 2,292 queued rows
leaving**; re-declaring the same two accounts reports **0 entering and 0 leaving**, so the
identity case is stable rather than reporting churn for no change.

**Declaring a large account you already depend on barely widens the crawl, and that is the
preview earning its place.** Adding `actions` moves roots **44 → 75** while the closure moves
**486 → 487** and queued rows do not move at all: **2,292 → 2,292**. The reason is that **30 of
the 31 `actions/*` repositories were already inside the closure**, reached transitively — checked
independently in SQL. Thirty-one more repositories declared, one more crawled, no more work. An
operator would have predicted the opposite.

**Declaring `advanced-security` names one repository entering:
`advanced-security/dismiss-alerts`** — the same repository `db/owned-scope.md` measured as the
only trace of a declared account in 56,830 nodes, still pending at depth 2. It is also correctly
marked *confirmed by gh* where the two stored accounts are not, so the advisory verification is
visibly doing its job rather than gating the declaration.

**An include list survives the round trip**: `facebook` restricted to `react` reads back
`includesOnly: true, includes: [react]`, and reports **0 roots and a closure of 0** — correct,
because the corpus holds twenty `facebook/*` repositories and `react` is not among them. That is
the *reaches nothing* case, on real data.

Nothing else moved: nodes **56,830**, edges **46,264**, findings **11,370** and frontier rows
**3,663** are identical before and after.

## What turned out wrong

**A preview cannot ask the store, and the obvious way around it deadlocks.** The declaration being
previewed has not been written, so the SQL predicate cannot answer for it. Writing it inside a
transaction and rolling back would be one derivation rather than two — and it hangs, because
`OwnedClosure` queries the pool while the transaction holds the single connection
`MaxOpenConns(1)` allows. So there is a Go form, `DeclaredAccount.Covers`, and
`TestCoveredByAgreesWithTheStoredScopePredicate` is what stops the two drifting. It was verified
by making `Covers` ignore `IncludesOnly` and watching it fail on exactly the two include-list
repositories.

**The test harness's stub ordering was load-bearing and undocumented.** `api/owned-scope` is a
prefix of `api/owned-scope/preview`, so the new endpoint was ambiguous under the harness's
"contains" matching. Resolving it as *longest match wins* looked obviously right and **broke
`NodeReachTests` and `NodeUsersTests`**, which depend on the specific `/-/users` beating the
generic `api/node/` — the opposite rule. Those tests had been relying on `Dictionary` enumeration
order, which nothing guaranteed. It is now an ordered list, first registered wins, and `Answer`
says so.

**Both text fields were bound to the wrong event.** `Immediate="true"` binds MudBlazor to
`oninput`, and a bUnit `Change()` raises `change`, so the include editor and the by-name field
both silently ignored input. Two failing tests, one cause.

**The verification passed while the premise was false.** Every check in this plan's Verification
section was satisfied by a declaration naming accounts the operator had never owned — including
"declaring an account with no repositories in the corpus is reported as reaching nothing", which
was exercised by restricting `facebook` to `react`. A verification that cannot fail on the data
being wrong is a verification of the mechanism only, and this plan had no check that the
declaration meant anything.

## What shipped after the operator read it

The editor was built, the operator opened it, and it immediately produced two corrections it had
no tests for:

- **Only `owner` and `admin` may be declared.** `gh` reports 33 accounts and the operator is
  answerable for 13. Offering all 33 is how `facebook` came to be declared at all. Decided once in
  `store.Account.CanDeclare` and carried to the interface as `canDeclare`; members are hidden
  behind *Show all 33* and never get a Declare control. Design and the deliberate deferral of
  finer roles live in `db/declaration-provenance.md`.
- **A Role column in the declared table**, carrying what `gh` reports or *not reported* — because
  the role is half of what "confirmed by gh" is confirming, and a declaration standing on a role
  that can change nothing is the state this page exists to expose.
- **One tooltip per button, and no prose beside a control.** `Save declaration` → **Save**,
  `Discard changes` → **Discard**, the captions moved into tooltips, and `Undeclare` gained a
  border because `Variant.Text` reads as plain text in dark mode until hovered. The post-save
  sentence became an **Open Discovery** button: naming the next action as a control rather than a
  paragraph.

## Open questions

- **Whether undeclaring should offer to prune.** The two are different operations —
  `db/pruning.md` is explicit that removal is separate and reversible only by re-analysis — but an
  operator who narrows their scope and sees no change anywhere will assume the narrowing failed.
  Pruning now has a home in the de-emphasized corner of Discovery that
  `ui/closure-first-explorer.md` builds; the question left is whether undeclaring should point at
  it, which is a link rather than a second control.
- ~~**Whether a declaration should trigger anything.**~~ Decided: a declaration `gh` confirms
  membership of enumerates its repositories immediately, with no second button, because the
  membership check has already established the only thing a button would be asking. An account
  `gh` cannot confirm stays declarable and is not enumerated, which is what makes the unconfirmed
  mark load-bearing. `ui/first-run.md` holds the design. Running *analysis* on a checkbox remains
  too large an unpriced action.
