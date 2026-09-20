# The same graph draws a different picture every time

**Status:** The server computes a repeatable layout and the browser does not read it yet. DOT
encoding, cluster subgraphs, the Graphviz seam, `/api/graph/export` and `/api/graph/layout` are
built and degrade by name where Graphviz is absent. The Explorer still draws a force simulation
with no fixed seed, so what a reader sees is still different every run.

## Why this exists

Two costs, and neither is frame rate.

**Nothing can assert anything about a picture.** `verify-abom-ui` reviews screenshots and `make
e2e` measures geometry, and both are looking at a layout that is different every run. That is why
the browser suite contains a test named *switching force preserves angle* — an angle is the only
thing about the current layout that survives a reload. A drawing that cannot be reproduced cannot
be regression-tested, and cannot be put in a document.

**Boundaries and relationships cannot be drawn at once.** `docs/roadmap.md` records it as a known
ceiling: the layout does not separate accounts well enough to draw a boundary around each without
enclosing somebody else's node, so the reader chooses between grouping and relationships. The
boundaries themselves are computed by hand — a convex hull, an inflation pass and a clipping pass
in `regions.js` — because a force simulation has no notion of a region.

Graphviz has solved both. `cluster` subgraphs report their own bounding box, `dot` ranks a directed
graph so distance from a root is a readable axis, and every engine is deterministic given a fixed
start. The useful view here is a few hundred nodes — a closure around a subset, not the corpus.
Which engine suits it is measured below rather than assumed, because the measurement contradicts
the guess twice over: boxes that stay apart are a property of `dot`, not of `cluster` subgraphs in
general, and the engine with the best-shaped canvas is the least readable of the set.

**What this is not.** Removing *every* edge from the whole-corpus view still reaches only 4.8 FPS,
so the ceiling is node count in the renderer and layout does not move it. Nothing in this plan is a
performance fix, and presenting it as one would be false.

## Pending

1. **Consume positions in the browser.** `applyLayout` fixes each node and releases the fix on
   drag, so the canvas, the hover, the selection and the delta merge are all untouched. Nodes with
   no position keep the force. `regions.js` draws a server bounding box when it has one. The pure
   half exists — `ui/wwwroot/js/serverlayout.js` fits positions to an extent, flips the axis
   Graphviz reports bottom-left-first, pins what was placed and reports what was not — and nothing
   in `graph.js` calls it yet. Depends on nothing further.
2. **Stop claiming position means something before it does.** The Neighborhood view captions itself
   "Position is structural: clusters are groups that share dependencies" in
   [`Explorer.razor`](../../ui/Pages/Explorer.razor), and it is the one view whose position comes
   from the force simulation with no semantic force behind it. Priority, Coverage and Distance earn
   their radial claims; this one asserts a meaning the picture does not carry, and the picture
   changes on every load. Either the caption says it is a settled simulation, or 1 lands and makes
   the claim true — for the nodes Graphviz placed, and only those. Depends on 1.
3. **A canvas that is pannable is not a canvas that is legible at 33 nodes.** `dot` returns a
   9.2:1 ribbon on the corpus projection. Scaling to fit keeps every node on screen and makes them
   small; the alternatives are `rankdir=LR`, `ratio=compress` with a `size`, or accepting the
   ribbon and relying on zoom. Undecided, and it wants a reader looking at all three rather than
   an argument. Depends on 1.

## What placing the real corpus measured

Graphviz 16.1.0, against the seven-repository corpus and a fanned fixture of the same shape.

**Only four of the seven engines box a cluster at all.** `dot`, `neato`, `fdp` and `osage` return
boundaries; `sfdp`, `twopi` and `circo` accept the subgraphs and return none. An engine that cannot
do it is not a failure, but a reader who grouped and saw no boundaries with nothing said would take
it for a corpus where nothing shares a group, so `/api/graph/layout` reports `groupingDrawn` either
way.

**The compact answer is the wrong one, and the numbers say the opposite of the picture.** `neato`
returns a canvas near a screen's proportions and `dot` returns a ribbon nine times wider than it is
tall, so on shape alone `dot` looks like the worse choice. Rendered, it is the only good one:
`neato` bought its proportions by stacking 75 pairs of nodes on top of each other, and with
`overlap=false` turned on to fix that, all 21 pairs of its group boxes crossed instead. `dot`
overlapped no nodes and crossed no boundaries. A wide canvas is a cost that scaling to fit and a
pannable canvas absorb; a group box drawn across another group box is unreadable at every zoom.

So the default is `dot`, and the test defending it asserts disjoint boundaries rather than a
pleasing aspect ratio. An aspect-ratio test was written first, selected `neato`, and had to be
withdrawn — it passed on a layout that a glance at the render showed was illegible.

## Why ABOM writes its own DOT rather than asking CodeQL for it

`codeql bqrs interpret` emits `dot`, `dgml` and `graphtext`, and the temptation is to use it. Two
findings say not to.

**CodeQL's DOT output omits properties.** The cryptography work hit this and fell back to DGML for
exactly that reason. A DOT file with no attributes carries a shape and none of the facts, which is
the opposite of what is needed here.

**`@kind graph` would destroy edge identity.** Its results are `nodes(e, key, value)` and
`edges(a, b, key, value)` — keyed by entity, with no location column. `normalize.EdgeKey` is the
source location that declares the reference, so two `uses:` lines to one target are two edges, and
an entity-keyed tuple merges them. That is not hypothetical: a fixture whose tuples all shared a
location once collapsed every edge to one row through `ON CONFLICT(edge_key)`, and a correct
enqueue looked broken for several rounds. `AbomEdges.ql` keeps its select.

`internal/ingest.ShapeGraph` is declared and has no handler. A shape that routes nowhere is worse
than a refusal, and this plan settles which it is.

## The constraints that must not be broken

**DOT has no null, and an absent measurement is still not a zero.** A node with no
`abom_blast_radius` row must not be written with `dependentRepos=0`; it carries an explicit
statement that its reach is unknown. This is the one place the format actively invites the wrong
answer.

**Never `strict`.** A strict digraph silently collapses multi-edges, which is the same identity
loss described above, arriving through the syntax instead.

**A scoped export says what it left out.** The predicate and the counts are graph-level attributes.
An export that quietly drops what did not match is the coverage lie a performance fix turns into.

**Quote and escape by the specification, not by intuition.** Inside a quoted DOT string only `"` is
escaped — `\\` stays `\\` — and a line beginning with `#` is discarded as a preprocessor marker.
Node keys carry paths and job identifiers from files ABOM did not write.

**A force engine is only deterministic with an explicit start.** `fdp`, `sfdp` and `neato` seed
themselves otherwise, and the whole reason for this plan evaporates without it.

## Verification

- Export twice; `diff` is empty. Lay out twice; positions are identical.
- `dot -Tsvg` and `sfdp -Tsvg` both parse the output without a warning.
- A repository whose name contains a quote round-trips through the encoder.
- Two `uses:` lines to one target survive as two edges.
- A node with no reach row exports as unknown and is not drawn at the smallest size.
- With Graphviz removed from `PATH` the Explorer still draws, and says the force layout drew it.
- Nothing in this plan moves node, edge, reach, exposure or bill-of-materials totals.
