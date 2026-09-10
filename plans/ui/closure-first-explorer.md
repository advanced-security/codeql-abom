# The Explorer starts from what you own

**Status:** the Explorer arrives at the closure of what the operator owns, filtered to what has
findings, grouped by owner and open. The whole corpus is not offered as a view.

## Why this exists

Legibility is a property of scope, not of layout. Drawn over everything the crawl walked through,
the Explorer is a saturated starburst in which no edge can be followed end to end, and the
boundary classification paints it one color because almost every edge crosses accounts. **A
classification is only legible inside a scope small enough for the distinction to vary.**

So the arrival scope is two conditions. The closure alone is 4,805 nodes on the reference corpus
and never settles; with `has findings` it is **715 nodes across 130 repository groups**, which
draws and can be read — and it is the question the page exists to answer: not what do I depend on,
but what do I depend on that something is wrong with.

## Pending

1. **The Coverage force lays out from a boolean.** It cannot separate 1 reference of 27 from 27
   of 27, the exact distinction it is named for. The ratio is already on every node as
   `refsInUse` / `refsAnalyzed`; see `ui/explorer-references.md`.
2. **The Reference filter's relationship to analyzed coverage.** It filters edges by observed ref;
   decide whether filtering to `v4` also restricts what "analyzed" means on screen. Which edges
   are drawn and what a node claims about itself are two questions with one control today.

## The constraint that must not be broken

**A default that scopes and filters has to report both exclusions.** The arrival hides what is
outside the closure and what inside it has no finding; a counts sentence carrying one number
asserts a clean estate for everything it dropped, in the grouped branch as much as the ungrouped.

## Verification

- On the reference corpus, an edge can be followed end to end at the arrival scope.
- With nothing declared, the Explorer is empty and says why rather than drawing the corpus.
- With discovery running against a declared scope, a node arriving between two polls can be seen
  to appear. Unverified; it needs a session that can run a crawl.

## Open question

**What the empty state offers.** Pointing at Settings is the minimum. Enumerating a declared
account's repositories is an API call rather than an analysis, so the Explorer could hold accounts
and repositories seconds after a declaration, which also makes an arrival visible without a crawl.
