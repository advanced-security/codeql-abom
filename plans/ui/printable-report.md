# A report somebody can hand to somebody else

**Status:** Not started, and no longer fully blocked. A deterministic layout exists on the server —
[`ui/graph-layout.md`](graph-layout.md) built the DOT export and the Graphviz seam, and Graphviz
renders SVG directly — so the figure no longer waits on the browser drawing the same picture twice.
What still waits on that plan is the Explorer itself. The server-side SVG writer exists for one
node's closure (`internal/figure`, drawn on every details page and printed with it); a scope-sized
figure reuses it.

## Why this exists

Everything ABOM knows is reachable through a screen that answers one question at a time. A security
team reporting upward, or handing a finding to the team that owns it, needs both shapes of the same
scope in one artifact: **which things match this**, and **what do they touch**. Today that is two
surfaces, two addresses and a screenshot somebody took by hand.

The bill of materials is already half of this. It is a *document* rather than a listing — it states
its own coverage, tells direct from indirect, distinguishes "not examined" from "none found", and
says that advisories are reported against a repository rather than evaluated against the references
in use. Those are claims no grid makes, which is exactly why it is a rendering of its own.

What it has no way to carry is the picture. The relationships are the reason a transitive finding
matters at all, and a table of hops is not how anybody reads a supply chain.

## Pending

1. **One scope, two renderings, one document.** The report takes the predicate the reader has
   already narrowed to, and renders the Inventory unit as a table and the Explorer closure as a
   figure — the same scope object both surfaces use, so the two halves cannot describe different
   sets. Depends on `graph-layout`.
2. **A static rendering of the graph.** A deterministic layout produces an SVG rather than a live
   canvas, which is what makes a figure reproducible and printable at all. The pieces exist:
   `/api/graph/export` emits the scoped projection as DOT and Graphviz renders SVG from it, so
   this needs no browser. What it does need is a shape that fits a page — `dot` returns a 9.2:1
   ribbon on the corpus projection, which a pannable canvas absorbs and paper does not, and that
   is [`graph-layout`](graph-layout.md)'s open aspect-ratio question rather than a second one.
   The writer is `internal/figure`'s, and a details page already prints its own figure in the light
   palette without the application around it. What this plan adds is the scope-sized figure, which
   waits on graph-layout's shape decision.
3. **Print styling that is a rendering, not a media query bolted onto the application.** Page
   breaks that do not split a table row from its header, a figure sized to a page rather than to a
   viewport, and no interactive chrome. Depends on 1.
4. **The document states its own scope, its coverage and its moment**, in the way the bill of
   materials already does. A printed artifact outlives the session that produced it, so a report
   that does not say what it excluded is worse than a screen that does not. Depends on 1.

## The constraints that must not be broken

**A report is a rendering of a scope, never a fifth surface.** It owns no predicate of its own and
offers no narrowing the Inventory and the Explorer do not. The moment it does, a reader who
narrowed somewhere else cannot carry that narrowing into it, which is the failure a page-per-action
always produces.

**A printed figure must say how much of the graph it is showing.** A canvas can be panned and a
page cannot, so a closure that did not fit is a coverage claim the document has to make out loud.

**An absent measurement survives printing.** A node with unknown reach is not drawn, aggregated or
tabulated as a zero because the format has no room for the distinction.

**No new capability in the client.** If the report can render it, an endpoint can produce it.

## Verification

- The same predicate produces the same document twice, byte for byte where the format allows it.
- The table and the figure describe the same set, asserted by count rather than by inspection.
- A closure too large for a page is reported as truncated, with the number withheld.
- Printed from a real browser at a real paper size, not only rendered to a viewport that happens to
  be page-shaped.
