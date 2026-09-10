# The Explorer speaks in references

**Status:** the node key is `name@ref`, so one node is one reference; nodes carry `refsInUse` and
`refsAnalyzed`, and the "not analyzed" filter counts partial coverage.

## Why this exists

The queue analyzes a dependency *at a reference*: `abom_frontier` is keyed `(node_key, ref)`, and
on the reference corpus **3,298 `name@ref` pairs are in use, 574 analyzed, 2,668 never queued**. A
payload that collapses those into one boolean calls `actions/checkout` analyzed on the strength of
1 reference out of 27, and excludes it from the filter whose whole purpose is finding unanalyzed
things. Silence reading as completeness is worse than the gap it hides, because the interface
looks like it checked.

The model itself is correct and unchanged — a node is an identity, a reference is an edge
attribute. What is left is the parts of the interface that still read a boolean.

## Pending

1. **Two force buttons render disabled and say nothing about why.** `WHO CAN FIX IT` and
   `DISTANCE` are dimmed while the other three are live, so the control reads as broken rather
   than as unavailable.
2. **Seed and upstream are not distinguished in the graph.** `origin` is a filter and a node
   property; color encodes kind. Whether the distinction has to be visible in the layout or
   whether the filter is enough needs deciding rather than leaving to chance, because conflating
   the two is a stated invariant — see `docs/architecture.md`.

The Coverage force and the Reference filter's scope are the other two consumers of the ratio; both
are tracked in `ui/closure-first-explorer.md`.

## Deferred by decision

**Collapsing `name@ref` back to one node per action.** Per-reference nodes are the useful default:
the key carries the version, so a node labels itself. A collapsed reading — one node per action,
references summarized on it — is legitimate for "which actions do we use at all", and the pieces
exist, since `RefsInUse` and `RefsAnalyzed` already ride on every node and `collapse.js` already
sums them for groups. It stays unbuilt because nothing has needed that reading, and split remains
the default if it is ever added.

## The constraint that must not be broken

**A node may not report coverage it does not have.** Partial coverage is its own state, not a
rounding of `analyzed` in either direction: an action at 1 of 27 must not be reachable by the same
reading as one at 27 of 27. **References stay verbatim** — `refs` on an edge is the observed
string, and nothing may normalize or truncate it on the way to a node.

## Verification

- The corpus is the only place this shows: a fixture would have to be *designed* to hold an action
  used at 27 references with 1 analyzed, which is how the gap survived being built.
- Take any node the store reports as mixed and confirm the interface distinguishes it from a fully
  analyzed one — by looking, not by reading the payload.
- `mergeInto` and `collapseGroups` both carry the counts. A live delta or a fold that drops them
  silently restores the boolean.
