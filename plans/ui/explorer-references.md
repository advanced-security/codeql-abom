# The Explorer still speaks in names, not references

**Status:** shipped. Nodes carry `refsInUse`/`refsAnalyzed`, the "not analyzed" filter means what
it says, and the node page states a ratio rather than "yes". The Coverage force and the Reference
filter's scope are still open.

## Why this exists

The queue analyzes a dependency *at a reference*: `abom_frontier` is keyed `(node_key, ref)`, and
on the real corpus **3,298 `name@ref` pairs are in use, 574 analyzed, 2,668 never queued**. The
graph model is unchanged and correct — a node is an identity, a reference is an edge attribute —
but the Explorer's *node payload* was written when one dependency meant one analysis, and it still
collapses every reference into a single boolean.

Measured on `/api/graph/full`:

| | |
| --- | --- |
| `actions/checkout` reference rows | **27** |
| analyzed | **1** |
| what the Explorer is told | `analyzed: true`, `discovery: "queued"` |

The **"not analyzed" filter reads `analyzed`**, so an action that is 96% unanalyzed is excluded
from the filter whose entire purpose is finding unanalyzed things. That is silence reading as
completeness, and it is worse than the gap it hides because the interface looks like it checked.

`analyzed` and `discovery` also now disagree by construction: `analyzed` is `EXISTS(status='done')`
— true if *any* reference is analyzed — while `discovery` aggregates so the *least settled*
reference wins. Both are individually defensible and together they let one node be described as
analyzed and queued at once, with no vocabulary for "1 of 27".

## The constraint that must not be broken

**A node may not report coverage it does not have.** This is the same rule as an unmeasured node
not being drawn at the smallest radius: partial coverage is its own state, not a rounding of
`analyzed` in either direction. Whatever the Explorer shows, `actions/checkout` at 1 of 27 must not
be reachable by the same reading as one at 27 of 27.

**References stay verbatim.** `refs` on an edge is already the observed string; nothing may
normalize or truncate it on the way to a node.

## Pending

1. ~~Per-node coverage in the payload.~~ **Shipped** — `refsInUse` and `refsAnalyzed` on the full
   graph, the delta metrics and the node detail.
2. ~~A filter that means what it says.~~ **Shipped** — `fullyAnalyzed()` replaces the boolean, and
   "not analyzed" went from **8,662 to 9,129** nodes, the **467** difference being exactly the
   partially covered set counted independently in SQL.
3. **The Coverage force.** It still lays out from the boolean rather than the ratio, so it cannot
   separate 1/27 from 27/27 — the exact distinction it is named for. The data it needs is now on
   the node.
4. **The `Reference` filter's scope.** It filters edges by observed ref. Now that nodes carry
   coverage, it needs deciding whether filtering to `v4` should also restrict what "analyzed"
   means on screen, which is a different question from which edges are drawn.

## What it measured

| | before | after |
| --- | --- | --- |
| nodes reachable by "not analyzed" | 8,662 | **9,129** |
| partially covered nodes it was hiding | — | **467** |
| `actions/checkout` | `analyzed: true` | `refsInUse: 27, refsAnalyzed: 1` |

The 467 was counted twice by different routes — once in SQL against `abom_frontier` before any code
changed, once from the rendered payload afterwards — and the two agree exactly.

**Two places would have dropped the counts silently.** `mergeInto` copies metric fields onto
existing nodes when a delta arrives, so coverage had to be added there or a live crawl would have
reset every node to whatever the first full load said. And `collapseGroups` folds nodes into
groups; without summing the counts a group of partially covered dependencies would have reported
its members' `analyzed` boolean and re-hidden exactly what this change exposes.

**`analyzed` was deliberately left alone.** It answers "is anything downstream of this known",
which is a real question with a boolean answer, and the temptation was to redefine it as "fully
covered" and be done. That would have changed the meaning of a field several callers already read
correctly. The ratio is additive; the boolean keeps its meaning.

## Other findings from the same pass

Kept here rather than lost in a conversation. None is in the same class as the coverage gap above.

- **Two force buttons render disabled** — `WHO CAN FIX IT` and `DISTANCE` are visibly dimmed while
  `NEIGHBORHOOD`, `PRIORITY` and `COVERAGE` are live. Whatever disables them says nothing about
  why, so the control reads as broken rather than as unavailable.
- **Hub occlusion at 277 nodes.** The periphery is followable — a single edge can be traced from
  the outer arcs to its end — but the two central hubs are dense enough that individual edges
  cannot be separated. The scope claim in `ui/performance.md` ("at 277 unfolded nodes an edge can
  be followed end to end") holds at the edges of the layout and not at its center.
- **Node labels collide.** ~~Two labels overlapped mid-render~~ — **fixed, and it was not a
  collision.** Two labelling systems were running: force-graph's built-in `.nodeLabel` HTML
  tooltip and this view's canvas `drawLabels`. Both drew the same node, at different positions
  and with different text (`actions/download-artifact action · 77 repos` versus
  `actions/download-artifact`), and because the tooltip is positioned from its own hover events
  it kept naming the last node it saw once a selection existed — reading as a label that had
  stopped tracking the pointer. The tooltip is now off and the canvas label carries the reach it
  used to add. `labelFor` is tested.
- **Seed and upstream are not visually distinguished in the graph.** `origin` is a filter and a
  node property; colour encodes *kind*. Whether the graph needs to carry the distinction visually
  or whether the filter is sufficient is worth deciding rather than leaving to chance, since
  conflating them is a stated invariant.

## Collapsing a name back to one node

Requested, not yet built. Per-reference nodes are the useful default and now the only behavior;
`name@ref` is the node key, so a version labels itself. A collapsed view — one node per action,
references summarized on it — is a legitimate second reading for "which actions do we use at
all", and the pieces are in place (`RefsInUse` / `RefsAnalyzed` already ride on every node, and
`collapse.js` already sums them for groups). What is missing is the option itself: a
`FullGraphOptions` field and an Explorer control, with split remaining the default.

## Verification

The corpus is the only place this shows: a fixture would have to be *designed* to contain an
action used at 27 references with 1 analyzed, which is how the gap survived being built. Check any
node the store reports as mixed and confirm the interface distinguishes it from a fully analyzed
one — by looking, not by reading the payload.
