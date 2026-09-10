# One Discovery configuration box

**Status:** the run controls, the re-analysis buttons and the per-tier suite binding are one box,
and a folded section states what it is set to in its header.

## Why this exists

The Discovery page answers two questions for two readers. *What the crawl is doing right now*
changes every few seconds and is why the page is open. *What the crawl is configured to do*
changes a few times a month, and every control added for it arrives as another outlined panel
stacked above the first.

The cost lands on the thing a reader came for: the queue. Consolidation pays for itself in height
only above about 1600 CSS px — below 1280 the run-control row wraps and it is roughly a wash — so
the remaining work is about what the box holds rather than about the box.

## Pending

1. **Re-analyze a selection, not only a whole tier.** The Discovery table already filters; acting
   on the current filter is the natural extension. It is also the control that spends minutes of
   cloning and CodeQL database building, so it must state the size of what it is about to re-run
   before it runs it. The per-tier controls it extends are in `ui/tier-suites.md`.
2. **Decide what a section whose read failed does.** A panel that could not reach
   `/api/discovery/suites` has no setting to summarize. It either refuses to fold or carries the
   error in its header; today it is neither by design.

## The constraint that must not be broken

**A collapsed section must still state what it is set to.** The rule against printing a number
nobody measured applies to controls as much as to data: a fold that hides the binding turns
"direct runs findings" into something a reader has to go looking for, and a reader who does not
know to look assumes the default. The header carries the current setting —
`direct: dependency-graph, findings · transitive: dependency-graph` — and only the means of
*changing* it folds away. Collapsed is a sensible default only because the summary is not.

**A remembered fold state is per reader, not per corpus.** If expansion is persisted at all,
`localStorage` is the right home; in the database it would read as configuration that affects the
crawl.

## Deferred by decision

- **Moving configuration to its own route.** It belongs beside the thing it configures: a reader
  deciding to re-analyze wants the queue depth on screen while deciding.
- **Folding the run controls.** They are the answer to "what is it doing", which is why the page
  was opened.

## Verification

- Measure at 1280, 1600 and 1920: the frontier table starts inside the viewport with the
  configuration collapsed, and nothing in the run-control row is clipped when it wraps.
- Read `window.innerWidth` in the same call as the geometry. A requested viewport is not the
  viewport you get, and a screenshot taken at a width nobody set shows controls simply absent.
- The screenshot pass, because `ui-test` has no CSS engine and has been green through every layout
  defect this page has had. MudBlazor's specificity and flexbox traps are in
  `.github/instructions/ui.instructions.md`.
