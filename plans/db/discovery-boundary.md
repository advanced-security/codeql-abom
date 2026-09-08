# Nothing enters the graph that the owned set does not reach

**Status:** shipped, without the `origin` rename the plan asked for — which reconciling showed
would have been a false claim. Discovery is bounded by the owned closure, and the round found a
latent defect that would have swallowed the entire root set.

## What reconciling corrected, before any code changed

**The rename would have asserted something untrue.** The plan said "`seed` becomes `owned` — it is
the same set under a name that says why". `db/owned-scope.md` had just measured that the two are
**disjoint on this corpus**: 0 of the 204 `origin='seed'` repositories belong to a declared
account. Renaming would have recorded 204 repositories nobody owns as owned. `origin` keeps
meaning *how a repository entered the corpus*, and the boundary is a separate question answered
from the declaration.

**The closure machinery already existed in another form.** `graph.ClassifyDependencies` already
walks a reachability closure breadth-first — rooted at `origin='seed'` containers, answering
whether anything under review *executes* a dependency. `OwnedClosure` is the same shape rooted at
the declared accounts and asking whether it is reachable at all, kept separate because they are
different questions.

**There are exactly three production `Enqueue` callers**, all in `expand`: `SeedFrontier`,
`enqueueDiscoveries` and `SweepDiscoveries`. The boundary went into all three rather than into
`Enqueue`, which would have needed a per-item lookup in a hot loop.

## The defect this round found

**`plansByTier` built plans for direct and transitive only.** The owned tier was added to
`tierOf`, to `abom_tier_suite`'s seed, and to the Discovery page's columns — and never here. A
batch landing in it got a zero-value `tierPlan`, which `pendingWork` reads as nothing
outstanding, so `prepare` returned `AlreadyRecorded: true`.

**An owned repository was settled as *already recorded* without a query ever running against it.**
The test that caught it reports `Processed:0 Skipped:1`.

It was latent because the owned tier held accounts the corpus contained almost nothing from: all
221 done owned rows have query runs, because they were analyzed before they became owned. After
this plan **every root is owned-tier**, so it would have silently swallowed the entire root set on
the first real crawl. Four test harnesses enumerated the tiers the same way and were corrected
with it.

## What the corpus check found

Declaring seven accounts the operator is owner or admin of:

| | |
| --- | ---: |
| owned repositories present in the corpus | **1** |
| repositories inside the closure | **270 of 1,099** |
| queue rows inside the closure | **1,642 of 3,662** |
| repositories the boundary excludes | **829** |
| queue rows the boundary excludes | **2,020** |

The single owned repository is `advanced-security/dismiss-alerts`. It reaches 270 because it names
`actions/checkout`, `actions/setup-node`, `actions/upload-artifact` and `github/codeql-action`,
and those hubs' own repositories name a great deal more.

**The closure is deliberately wider than what gets queued.** It follows every dependency edge,
including an upstream action's own CI, because admission asks *is this reachable* and not *does
anything under review execute it*. The second question is `ClassifyDependencies`, which still
governs runtime versus build-time, and `TestExpandIgnoresDependenciesOfUnreferencedUpstreamCI`
still holds. A boundary that answered both questions at once would have made one of them
unreadable.

## Why this exists

There is no ownership boundary anywhere in discovery. `frontier.Queue.Enqueue` in
`internal/frontier/queue.go` drops a candidate on exactly two conditions:

```go
if it.NodeKey == "" || it.Depth > q.maxDepth() { continue }
```

and marks it `unavailable` when `namesARepository(it.Owner, it.Repo)` is false. That is the whole
predicate. Every dependency of every dependency of every seed is followed to depth five,
regardless of whose it is or whether anything the operator owns can reach it.

From 204 seeds that produced **56,830 nodes, 46,264 edges** and a queue still holding **813
pending** rows after 2,800 were analyzed. The graph is not wrong — every edge in it was really
observed — it is simply an answer to a question nobody asked, and it grows without anyone deciding
that it should.

## What is already true

| | |
| --- | --- |
| `DefaultMaxDepth` | **5**, flat |
| ownership checks in `Enqueue` | **none** |
| frontier rows | **3,662** (2,800 done, 813 pending, 49 unavailable) |
| nodes / edges | **56,830 / 46,264** |
| `origin` values | `seed`, `upstream` — and `seed` is the schema default |
| retroactive machinery | `SweepDiscoveries` and `Reclassify`, both drain-gated |

Two existing properties matter to the design:

`InsertUpstreamRepository` **never demotes a seed**, deliberately — "a repository can be both under
review and depended on by others, and demoting it would erase it from its own report."

`Queue.Reclassify` is **the only pass allowed to lower a claim**, because `Enqueue` may only ever
strengthen one so that arrival order cannot decide how a dependency is reported. The same argument
governs re-scoping: a boundary that tightens can never take effect through `Enqueue`, so it needs
the same whole-graph pass.

## The design

### The boundary

A candidate is admitted only when the thing that discovered it is inside the owned closure — an
owned repository, or something already admitted. The closure is therefore defined by induction
from the declared root set, and nothing outside it can pull anything in.

This is not a filter on *what* a dependency is. An action belonging to `actions` is followed
happily, because an owned repository names it. The rule is about the *route*: an action reached
only from a repository nothing owned depends on is not part of anyone's bill of materials.

### `origin` gains a third answer

`seed` becomes `owned` — it is the same set under a name that says why. A repository the boundary
excludes becomes out-of-scope: not `upstream`, because upstream means "fetched to resolve
something owned", which is exactly what these are not.

Renaming is worth its migration because `seed` is where the accidental default lives, and a column
whose default silently makes a root-set member is the defect in `db/owned-scope.md`'s first
paragraph.

### Iterative deepening replaces the flat cap

`MaxDepth = 5` is a truncation: it decides the answer is complete when it is merely five hops old.
A bill of materials wants the full closure.

So the cap becomes a *current* value that rises. The crawl runs to closure at depth 1, then at
depth 2, and so on until a pass adds nothing. Three properties follow, and they are the reason for
the shape:

- **A shallow answer is complete before a deep one is attempted.** The dependencies an owned
  repository names directly are the ones anyone can act on, and they are finished first.
- **The budget is spent nearest first.** Under a rate limit, the work that gets done is the work
  that matters most.
- **Closure is observable.** "The queue drained at depth 4 and depth 5 added nothing" is a
  statement about the graph; "we stopped at 5" is a statement about a constant.

The configured cap remains, as a backstop against a pathological chain rather than as the scope
rule.

### Re-scoping what already exists

`SweepDiscoveries` re-applies the discovery rule to every analyzed repository, and `Reclassify`
re-derives claims over the whole graph. Both already run on a drained queue. The boundary joins
them: a pass that marks every repository, node and edge outside the owned closure as out-of-scope,
computed wholesale so arrival order cannot affect it.

**Marking, not removing.** Removal is `db/pruning.md`, and it is a separate plan because deleting
from append-only tables is a separate problem with its own failure mode. This plan stops the graph
growing and says what does not belong; the next one takes it out.

## Phases

1. The closure predicate, as a store read, tested against the fixture before anything calls it.
2. `Enqueue` consults it. New crawls are bounded from here.
3. `origin` renamed and the out-of-scope value added, additively.
4. The whole-graph re-scope pass, joining `Sweep` and `Reclassify` on a drained queue.
5. Iterative deepening in the supervisor's cycle, replacing the flat cap.

## Verification

- The closure starts at what is declared, follows dependencies outward, and admits nothing the
  declared set does not reach. Verified by breaking the walk so it followed no edges and watching
  two of the six tests fail before restoring it.
- **With nothing declared the closure is empty, not universal.** Admitting everything would make
  the boundary inert exactly when it has never been configured, which is the state a first run is
  in — and an inert check looks exactly like a correct one that never fires.
- Ordering edges admit nothing: `job-needs-job` is sequence, not dependency.
- The sweep no longer reaches outside the closure. It re-applied the discovery rule to every
  repository in the table, including the 895 upstream ones nothing owned reaches.
- An owned repository is analyzed rather than settled unread.
- Seeding starts from the declaration rather than from `origin`.

Sixteen existing tests were **narrowed rather than deleted**. All shared one premise — the
repository being analyzed is under review — which is now stated as a declaration rather than
assumed. `harness.enqueue` declares each item's account, because a directly enqueued item is a
root and roots come from the declared scope; `enqueueUnowned` exists for the case that is
deliberately outside it. Two others were decoupled from a requeued row count that content
equivalence and tier membership legitimately change.

## Still pending

**Iterative deepening is not built.** The flat `DefaultMaxDepth = 5` remains. The boundary makes
it far less load-bearing — depth was the only guardrail and is now the second one — but running to
closure at depth 1 before spending anything at depth 2 belongs with the claim ordering in
`db/freshness-budget.md`, which is where the objective lives. Splitting it there rather than
leaving it half-done here.

**The out-of-scope marking is not built either.** The boundary stops the graph growing; it does
not mark what is already outside. That is `db/pruning.md`, which has to solve tombstoning anyway,
and marking without a way to act on the mark is a column with no consumer.

## Open questions

- **Whether an out-of-scope repository's findings stay attributed.** They were really produced.
  The plan's assumption is that the ledger keeps them and only the graph is re-scoped, which is
  also what `db/pruning.md` assumes; if that is wrong both plans move together.
- **Whether the cap should rise automatically or on request.** Automatic is what "as up to date as
  possible under budget" implies, but a crawl that deepens on its own is one that can surprise an
  operator with a bill. Deciding this belongs with `db/freshness-budget.md`.
