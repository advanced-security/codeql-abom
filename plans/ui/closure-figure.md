# A details page draws what runs it and what it runs, the same way every time

**Status:** Built. Every workflow's and action's page draws its closure, placed by Graphviz, which
`serve` requires; the figure applies the terms the page's own tables apply and refuses the rest,
where the Explorer would apply more. [`docs/architecture.md`](../../docs/architecture.md) says what
it does.

## Why this exists

A details page answered in tables, and the reader assembled the picture in their head. The Explorer
draws it, but not in a way anyone can keep: with Stable layout off it moves on every reload, and on,
a neighborhood collapses into a line ([graph-layout](graph-layout.md)). Around one node a ranked
layout means something — what runs it on one side, what it runs on the other.

## Pending

1. **The Explorer's terms on the figure.** The figure applies exactly the `uses` surface's terms,
   the same as *What `uses:` this* under it, and refuses the rest by name, so a page arriving from a
   findings-only Inventory view shows a refusal in the figure's place. The fork:
   - **Apply graph terms to the figure alone**, `flags=findings` keeping only routes to a finding
     or an advisory and the page's node — "Yours and what it runs, with findings" cut to one node —
     and say on the page that the tables under it apply fewer.
   - **Keep one set of terms for the whole page**, as now, and offer the findings-only cut as a
     control of the figure's own rather than a term.
2. **Precompute at a rebuild, if drawing on arrival stops being cheap.** Drawn on arrival and
   cached per graph generation, 157 figures measured p50 0.318 s and p95 0.343 s cold on the
   crawled organization. Precomputing means a figure for every workflow and action — about 100,000
   workflows at 20,000 repositories. Revisit when the endpoint's p95 passes 1 s, or when
   [printable-report](printable-report.md) needs many figures at once.

## The constraints that must not be broken

**The closure is directed.** Walked undirected, it fills with what else runs what this runs.

**What runs it is what the page lists.** Routes start where *What `uses:` this* rows start, under
the same terms, so the figure and the table cannot disagree about who runs a node.

**The figure holds at most 300 nodes, and the page says what it left out.** That means the
versions, the nodes beyond the cap, and what runs it from repositories the terms leave out.
Nothing is refused for size, and nothing is cut silently. Those sentences stay off the drawing,
with the Graphviz version and which side is which: the heading already says the picture, and the
version is what the cache key reads.

**A drawn node is connected to the page's node.** A node floating without its path asserts a
relationship the figure does not show.

**Yours is the declaration.** "Owned first" orders by the predicate `scope=owned` selects by, never
by `origin`.

**A reference is verbatim.** A label shortened to fit carries the whole reference in its title, and
the tables below keep it untouched.

**Advisories enrich, and never adjudicate.** A badge says how many advisories name the repository,
never that the version in use is affected.

**An absent measurement is not drawn as zero, or as clean.** A version not analyzed is drawn as not
analyzed, and a page that was not told how many `uses:` lines name it does not say none do.

**A category has a channel besides hue.** Severity pairs its dot with a word, and kind keeps the
Explorer legend's glyph.

**The figure speaks the reader's words.** "Folded", "hops", "reach", "frontier", "corpus" and
"generation" stay off it, and no text is turned on its side.

**Byte-identical within one Graphviz version, structurally identical across versions.** A golden
SVG compared across machines would fail on the first Graphviz upgrade. The version is what the
cache key reads. It is not painted.

**No key enters the DOT document.** DOT has no quoted form for a key ending in a backslash, so
nodes are written as synthetic identities and mapped back. Everything a key reaches in the SVG is
escaped as XML text, attribute or URL path segment.

**A missing Graphviz is a broken installation, and says so.** `serve` will not start without it,
and a running server that loses it answers 503 with the remedy. Neither falls back quietly.

## Verification

- **Item 1:** a page opened with `flags=findings` draws only routes to a finding or an advisory and
  the page's node, or says why it does not; the tables under it say which terms they applied.
- **Item 2:** `drawall` over every owned workflow and action and the ten most used actions keeps
  p95 under 1 s, cold, after a restart, and every drawing byte-identical to the last pass.
