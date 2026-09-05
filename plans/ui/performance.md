# Explorer performance

**Status:** measured, not yet addressed. The numbers below are the starting point.

## Why this exists

The Explorer's default view is unusable, and it is unusable before anyone interacts with it.
Opening `/explore` on the reference corpus renders **3.4 frames per second** — 17 frames in five
seconds. Typing one filter takes **7.5 seconds** to settle. The tab holds **419 MB** of JavaScript
heap on arrival and climbs past **640 MB** while sitting still.

The screenshot says the same thing the profiler does: the default view is a solid radial mass in
which no single edge can be followed to its other end, no node is distinguishable across the
middle 60% of the canvas, and the interaction hint printed at the bottom is overprinted by edges.

## What is already true, measured

Reference corpus: 204 seed repositories, 893 upstream, 54,508 nodes, 35,443 edges.

**The server is not the bottleneck.** Every endpoint answers in well under a second:

| Endpoint | Bytes | Server time |
| --- | --- | --- |
| `/api/graph/full` | 8.0 MB (473 KB gzipped) | 343 ms |
| `/api/graph/full?steps=true` | 28.7 MB | 533 ms |
| `/api/graph?focus=actions/checkout&hops=1&granularity=repository` | 328 KB | 126 ms |
| `/api/node/{key}/-/reach` | 29 KB | 71 ms |
| `/api/node/{key}/-/impact` | 17 KB | 33 ms |
| `/api/summary` | 326 B | 5 ms |

**The renderer is not the bottleneck either.** Frame rate is a pure function of how many nodes
are *visible*:

| Visible nodes | Frames per second |
| --- | --- |
| 9,811 (default) | **3.4** |
| 430 (`pytorch`) | **60.0** |
| 13 (`pytorch/vision`) | **60.1** |

Sixty is the display's ceiling, so the renderer has headroom at both filtered sizes. The cliff
sits somewhere between 430 and 9,811, and that range is the single most useful thing left to
measure.

**The bottleneck is client-side materialization.** The default payload is 9,811 nodes and 30,853
edges, all of which Blazor deserializes into objects and hands to Three.js before anything is
drawn. Filtering to 13 visible nodes moved the heap the wrong way — 593 MB to 643 MB — because
a filter is a visibility mask over a graph that stays fully materialized. Nothing is released,
so the cost is paid once and then kept.

**The payload is loose in three specific ways**, all measurable:

- **818 bytes per node**, as JSON objects with thirteen repeated string keys.
- **30% of edges carry no dependency information**: `repo-contains` (6,871) and `org-contains`
  (2,386) are containment, already implied by each node's own `repository` and `owner` fields.
- **63% of nodes are workflows** (6,144), the overwhelming majority of them leaves with a single
  edge.

## The diagnosis

One line explains the frame rate. `graph.js` gates the *simulation* set on how much is visible:

```js
const subset = visible.size < total * SUBSET_FRACTION;   // SUBSET_FRACTION = 0.25
```

With no filter active `visible === total`, so `subset` is false and `render()` hands all 9,811
nodes and 30,853 links to `graph.graphData()`. `paint()` then sets `nodeVisibility`, which only
stops a node being **drawn** — d3-force still integrates every node and every link on every tick.
Filtering to 430 nodes is 4.4%, crosses the 25% threshold, drops them out of the simulation, and
the frame rate goes to 60. The renderer was never the problem; the simulation set was.

The 7.5-second filter is the same threshold being crossed: a full `render()` that filters 9,811
nodes and 30,853 links, clones every link into `it.renderedFor`, reseeds positions, and then runs
`warmupTicks(420)` before presenting a frame.

The payload never enters WebAssembly. `loadFull()` does `fetch()` and `res.json()` in plain
JavaScript, so none of this is Blazor deserialization cost — it is retained JS objects and force
simulation work.

## The shape of the answer

The scoped endpoint that would fix this already exists and returns 328 KB —
`/api/graph?focus=&hops=&granularity=` — it is simply not what the Explorer loads. The work is
to make the default view a server-scoped query rather than a client-side filter over everything.

That the data is known ahead of time is the lever: the set of views, groupings and filters is
fixed and small, so scoping, aggregation and even layout can be precomputed rather than
recomputed per session.

## Pending

1. **State the cost of opening a fold before it is spent.** The repository rung is 1,220 nodes
   and comfortable; the workflow rung is several thousand and lands where the frame rate falls.
   The ladder currently lets a reader walk into that without warning. Same pattern as the
   re-analyze control, which puts the count on the button. Deliberately deferred until the
   exposure default has been used enough to say whether anyone reaches that rung at all.
2. **An encoding that survives folding.** Every edge between two owner groups is cross-owner by
   construction, so the class carries no information once folded. Less urgent now that the
   default view is unfolded, and the fold is a deliberate step rather than the arrival state.
3. **Stop shipping containment as edges.** 9,257 of 30,853 are `repo-contains` and
   `org-contains`, synthesized by a `UNION ALL` in `graphEdges` and already implied by each
   node's own `repository` and `owner`.
4. **Server query shape, for the unscoped view.** `graphNodes` still runs about five correlated
   subqueries per node — an advisory `COUNT(DISTINCT)` plus frontier status, depth and score —
   which is most of the 331 ms the whole-graph view costs. The scoped default no longer pays it.
   `ReachOfNode` separately calls `Builder.load()`, a full in-memory graph load, per request.
5. **Steps are focus-scoped but not focus-*fetched*.** The toggle now requires a selection, which
   stops the global 54,508-node case, but the payload behind it is still
   `/api/graph/full?steps=true` at 28.7 MB. It should come from the scoped endpoint.

## Done

- **Arrive scoped to a question** — the default is `scope=exposure`: only the routes by which
  repositories under review reach an action an advisory names. **277 nodes and 486 edges, 14 KB
  on the wire against 473 KB, 45 MB of heap against 419, at 60.7 frames per second.** The scope
  is the *route*, not the affected action, because whether a fix is yours or an intermediary's is
  a fact about the path. Advisories anchor it and findings do not: findings touch 3,652 nodes,
  37% of the graph, which would reproduce the problem. They remain a filter within the scope.
  The scope is 180 workflows reaching 22 affected actions through 10 reusable workflows and one
  composite action, across 64 accounts.
- **The scope is applied in SQL, not to an assembled graph.** Filtering afterwards still paid for
  all 9,811 nodes and their per-node advisory and frontier subqueries in order to return 277:
  **331 ms → 44 ms**, for byte-identical output.
- **This, not folding, is what made the view legible.** At 277 unfolded nodes an edge can be
  followed end to end and the edge classes read again, which 603 owner groups never allowed.
  Folding bought frames; scope bought meaning.
- **Find the cliff** — the table above.
- **Stop simulating what is not visible** — `SUBSET_FRACTION` gone; the simulation set is always
  the visible set, and an unchanged set is repainted rather than reseeded, compared by an
  order-independent signature rather than by count.
- **Come to rest** — a settled layout with a still camera stops drawing. `still` stops the
  renderer outright; `idle` keeps drawing a few frames a second so a selection's directional
  particles still advance, because they mark which way a dependency points and that is worth
  keeping on a view nobody is touching.
- **Collapsed on arrival** — `groupBy=owner`, folded, so the first paint is 603 nodes.
- **The fold is a ladder, not a switch** — opening the owner fold steps to 1,220 repository
  groups with everything below them still folded, and only the last rung opens the whole graph.
  Shipped as a switch first, which meant the one control the arrival state depends on dropped the
  reader straight from 603 nodes to the 9,811-node view that cannot be drawn.
- **A key that matches the canvas** — the legend is a containment tree, and folding hides the
  rows whose nodes the fold removed rather than leaving them there looking switched off.
- **`reachKnown` and `analyzed` through a fold** — a group holding one unmeasured member no
  longer reports its reach as known, and one holding an unanalyzed member is no longer drawn as
  analyzed.

## What the measurements do *not* support

**Splitting into services would not fix this.** Every server-side number above is already
comfortably fast, and the crawl already runs in a separate loop. A microservice boundary drawn
through code that answers in 343 ms moves the same 8 MB across one more hop. If a split earns
its place it will be for a different reason — a dedicated layout/precompute service producing
item 6, or independent release cadence — and that case should be made on its own evidence rather
than inherited from this plan.

**The renderer does not need replacing.** It holds 60 FPS at 430 nodes. Anything that starts by
rewriting the rendering layer is optimizing the half that is already working.

## The constraint that must not be broken

**Aggregation must not turn an unmeasured node into a measured zero.** Every rollup, cluster and
precomputed scope has to carry `reachKnown` through it. A node ingested since the last reach
rebuild has no `abom_blast_radius` row, and neither does a node nothing references; in a view
where size means reach, drawing the first at the smallest size asserts "nothing depends on this"
about something nobody has looked at. `TestGraphTablesAreAppendOnly` and the watermark in
`abom_graph_generation` exist for this, and a summarization layer is exactly where it would be
lost.

**Scoping must stay honest about what it excluded.** The view already reports "Showing 430 nodes
out of 9,811 total (9,381 nodes hidden by filters)". Server-side scoping makes that harder and
more important: a bounded query that silently drops the rest would turn a performance fix into a
coverage lie.

## Verification

- Frames per second at the default view, on the reference corpus, at 1440×900. Currently 3.4;
  anything below ~30 is not an interactive view.
- Heap after load, and after ten filter changes. Currently 419 MB rising to 643 MB; it must stop
  rising.
- Time from keystroke to settled canvas. Currently 7,488 ms.
- The hairball test from `.github/skills/verify-abom-ui/`: one edge, followed to its other end,
  in the default view. Currently impossible.
