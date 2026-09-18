# The same graph draws a different picture every time

**Status:** Layout is computed in the browser by a force simulation with no fixed seed, so no two
runs agree and nothing downstream can depend on a position.

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

Graphviz has solved both. `cluster` subgraphs are non-overlapping by construction and report their
own bounding box, `dot` ranks a directed graph so distance from a root is a readable axis, and
every engine is deterministic given a fixed start. The useful view here is a few hundred nodes — a
closure around a subset, not the corpus — which is squarely what `dot`, `twopi` and `fdp` are for.

**What this is not.** Removing *every* edge from the whole-corpus view still reaches only 4.8 FPS,
so the ceiling is node count in the renderer and layout does not move it. Nothing in this plan is a
performance fix, and presenting it as one would be false.

## Pending

1. **An encoder from the graph projection to DOT.** `internal/graphexport`, mirroring
   `internal/bomexport`, over `store.Graph` — the scoped, filtered projection the API already
   serves. Sorted output so it is byte-stable and diffable. Depends on nothing.
2. **An export endpoint**, `serve.SerializeGraph` mirroring `SerializeBOM`, taking the same
   predicate terms as `/api/graph/full` through the same `reading(...)` declaration, or a term is
   refused on one path and not the other. Depends on 1.
3. **Cluster subgraphs for the active grouping** — account, repository, workflow — each uniquely
   named, because a graph and its subgraphs share one namespace. Depends on 1.
4. **A layout seam.** `internal/layout` with an engine interface, implemented by writing DOT to a
   temporary file and running a **fixed argv**. Depends on 1.
5. **Degrade, and say so.** With no Graphviz on the machine the Explorer keeps the force layout and
   the interface states which produced the picture. A silent fallback is a layout claim nobody can
   trust. Depends on 4.
6. **`/api/graph/layout`** returning the engine, the positions, the cluster bounding boxes and what
   it dropped. Depends on 4.
7. **Consume positions in the browser.** `applyLayout` fixes each node and releases the fix on
   drag, so the canvas, the hover, the selection and the delta merge are all untouched. Nodes with
   no position keep the force. `regions.js` draws a server bounding box when it has one. Depends on
   6, and on a corpus: [`ui/testing.md`](testing.md) says what is owed there is "enough nodes for a
   layout claim to mean something", and twenty is not.

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
