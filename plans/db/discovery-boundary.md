# Nothing enters the graph that the owned set does not reach

**Status:** planned. Depends on `db/owned-scope.md`, which makes "owned" a thing the code can ask
about. This plan is the guardrail itself.

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

- **How much of the existing corpus is outside the owned closure.** Measured on a copy, from a
  small declared scope: nodes, edges, frontier rows and repositories in and out. This is the
  headline number and it will be most of the corpus.
- A crawl from a declared scope reaches exactly the closure the same rule computes independently
  in SQL. Exact agreement, not a plausible ratio.
- Nothing outside the closure is ever enqueued: a fixture where an out-of-scope repository names a
  new dependency must add nothing.
- Depth 1 closes before any depth-2 work is claimed, observable in the cycle log.
- Re-running the re-scope pass changes nothing the second time.
- `InsertUpstreamRepository`'s no-demotion rule still holds: a repository that is both owned and
  depended upon stays owned.

## Open questions

- **Whether an out-of-scope repository's findings stay attributed.** They were really produced.
  The plan's assumption is that the ledger keeps them and only the graph is re-scoped, which is
  also what `db/pruning.md` assumes; if that is wrong both plans move together.
- **Whether the cap should rise automatically or on request.** Automatic is what "as up to date as
  possible under budget" implies, but a crawl that deepens on its own is one that can surprise an
  operator with a bill. Deciding this belongs with `db/freshness-budget.md`.
