# Data efficiency: measured cost of the graph, and what breaks first

**Status:** The correlated-subquery family is closed. Three items are open, one of them a
disagreement between a passing unit test and the stored data.

## Why this exists

The graph is collected, stored, traversed, filtered and shipped, and each of those costs grows
with what the crawl walked through rather than with anything an operator declared. The ceiling is
rarely where it is looked for: frame rate is a function of node count and of nothing else, and
removing *every* edge from the whole-corpus view still reaches only 4.8 FPS. This file is the
standing record for the review described in
[refine-abom-data-structures-and-algorithms](../../.github/skills/refine-abom-data-structures-and-algorithms/SKILL.md).

## Pending

1. **`ReachOfNode` calls `Builder.load()` per request** — a full in-memory graph load to answer
   one node's reach.
2. **Steps are focus-scoped but not focus-*fetched*.** The toggle requires a selection, which
   stops the global case, but the payload behind it is still `/api/graph/full?steps=true`.
3. **The frontier's repository arm and the stored data disagree, and the unit test passes anyway.**
   That arm requires `f.depth = 0`. Measured once against a corpus that has since been retired, no
   depth-0 rows existed at all, so no repository node reached a queue row through it — while
   `TestARepositoryCarriesTheAnalysisStateOfItsOwnQueueRow` passes, because it enqueues at depth
   0. Either the fixture is not a shape the crawl produces, or the arm is keyed on the wrong
   thing. Re-measure against a declared organization, then settle which before either is changed.

## The constraints that must not be broken

**Aggregation must not turn an unmeasured node into a measured zero.** Every rollup, cluster and
precomputed scope has to carry `reachKnown` through it. A node ingested since the last reach
rebuild has no `abom_blast_radius` row, and neither does a node nothing references; in a view
where size means reach, drawing the first at the smallest size asserts "nothing depends on this"
about something nobody has looked at. A summarization layer is exactly where that is lost.

**Scoping must stay honest about what it excluded.** The view reports "Showing 213 nodes out of
401 total (188 nodes hidden by filters)". Server-side scoping makes that harder and more
important: a bounded query that silently drops the rest turns a performance fix into a coverage
lie.

## Verification

- A frame-rate probe carries a `movedProof`, and one that does not is not a measurement. Clearing
  the rest state without calling `graph.resumeAnimation()` times `requestAnimationFrame` against a
  paused renderer and reads a flat 60 FPS everywhere.
- A query rewrite is checked row by row against the old form, and its guard asserts the **query
  plan** rather than the clock.
- An endpoint timing is taken against a binary that was rebuilt, and cross-checked against a Go
  benchmark of the store call underneath it: a handler cannot be five times dearer than the only
  thing it does.
