# One Discovery configuration box

**Status:** shipped and measured. Run controls, re-analyze and the suite binding are one box; a
suite line carries its pack version and query count and expands to the query ids.

## Why this exists

The Discovery page answers two different questions for two different readers, and it currently
gives them equal room.

*What is the crawl doing right now* changes every few seconds and is why the page is open.
*What the crawl is configured to do* changes a few times a month.

Every control added for the second question has been added as another outlined `MudPaper`
stacked above the first. Measured on the reference corpus at a 720px viewport:

| | height | top |
| --- | --- | --- |
| run controls (Start/Pause, Follow, re-analyze) | 96px | 198 |
| "What each tier is analyzed with" | 304px | 306 |
| frontier table — the actual work list | | **666** |

Configuration occupies 400px, the suite grid alone is 3.2× the run controls, and the table a
reader came for starts 54px above the fold. The page is 1,766px tall to show 400 queued
dependencies. Nothing here is wrong; it is all simply always open.

## What to build

One outlined box for Discovery configuration, holding collapsible sections. The run controls
stay outside it and stay always visible — they are live state, not configuration.

```text
┌─ Discovery ────────────────────────────────────────────────┐
│  ▶ Start   ▓▓▓▓▓▓▓▓▓░░  Analyzing 4 repositories at a time │   always visible
│  ☐ Follow every 1 min           Re-analyze direct (380) …  │
└────────────────────────────────────────────────────────────┘
┌─ Configuration ────────────────────────────────────────────┐
│ ▸ What each tier is analyzed with                          │
│   direct: dependency-graph, findings · transitive: graph   │   ← summary, always shown
│ ▸ …room for the next control without a fourth box          │
└────────────────────────────────────────────────────────────┘
```

## The constraint that makes this non-trivial

**A collapsed section must still state what it is set to.** ABOM's rule against printing a
number nobody measured applies to controls as much as to data: a fold that hides the binding
turns "direct runs findings" into something a reader has to go looking for, and a reader who
does not know to look will assume the default. So the header carries a one-line summary of the
current setting — `direct: dependency-graph, findings · transitive: dependency-graph` — and only
the means of *changing* it folds away.

This is the same distinction the queue controls already keep: a pause that takes effect between
cycles is not the same as paused, and a control that appears to have done nothing is a bug.

Three consequences fall out of it:

- **Collapsed is the sensible default**, but only because the summary is not. Without the
  summary line, collapsed-by-default would be hiding state, and the honest default would be open.
- **A section whose read failed must not fold shut over the failure.** The panel that could not
  reach `/api/discovery/suites` has no summary to show; it must open, or its header must carry
  the error. Deciding which is part of this work.
- **A remembered fold state is per reader, not per corpus.** If expansion is persisted at all,
  `localStorage` is the right home; it must never be stored in the database, where it would read
  as configuration that affects the crawl.

## Traps

`ui-test` has no CSS engine. A MudBlazor expansion panel renders its content into the markup
whether or not it is expanded, so `Assert.Contains("abom-findings", cut.Markup)` passes on a
collapsed panel and proves nothing. Tests here must assert on the *summary* — which is real
markup — and on the expanded/collapsed attribute, never on the presence of the body. Whether it
actually looks folded is the screenshot pass in `verify-abom-ui`.

MudBlazor's expansion panels bring their own padding, elevation and divider rules, which have
fought `abom-*` styling before. Expect to spend as long on making one box look like one box as
on the folding itself.

## Verification

Re-measure the table above. The frontier table should start well within the viewport with the
configuration collapsed, and the page should be shorter than 1,766px for the same 400 rows.
Then the screenshot pass, because none of that is a claim `ui-test` can settle.

## What it measured, and what was wrong

Folded, on the reference corpus at the same 720px viewport:

| | before | after |
| --- | --- | --- |
| configuration section | 304px | 62px |
| frontier table starts at | 666 | 517 |

The queue is inside the viewport instead of 54px below it, and four rows are readable without
scrolling. Expanding puts the table back at 758 — which is the reader's choice, made knowingly.

Two things did not go as written:

- **The expansion-panel trap was avoided rather than handled.** The plan assumed MudBlazor's
  `MudExpansionPanels` and warned that its content renders whether or not it is expanded, so a
  markup assertion proves nothing. Rendering the body under a plain `@if` instead means a folded
  grid is genuinely absent, so `Assert.DoesNotContain("abom-tier-head", …)` is a real check.
  A hand-built `<button>` with `aria-expanded` also keeps the keyboard behavior that was the only
  reason to reach for the component.
- **`ui-test` was green while the header was visibly wrong.** MudBlazor centers button content
  globally, so the folded header sat indented to the middle of the panel. Nine passing component
  tests said nothing about it; the screenshot did, immediately. Exactly the split this repository
  already documents, met head on.

One number moved that should not have: findings 4,731 → 4,734 across a UI-only change. All three
came from `abom-findings.qls` at 14:00:06–14:00:12, the tail of the cycle that was still
finishing while discovery sat in `pausing`. A pause that takes effect between cycles is not the
same as paused — visible here in the data rather than in the control.

## What one box actually measured

Merging the two panels did not save vertical space on its own; it cost some. A 1px `MudDivider`
with `my-3` spent 24px, and `white-space` on the re-analyze labels was absent so MudBlazor's
uppercase transform wrapped "RE-ANALYZE DIRECT (423)" onto two lines. 223px in one box against
about 197px across two.

Tightening the divider and the labels recovered it, and the result depends on width more than on
anything else. All at a 720px viewport:

| width | box | frontier table starts at | rows visible |
| --- | --- | --- | --- |
| 1280 | 216px | 540 | 2 |
| 1600 | 150px | 441 | 7 |
| 1920 | 150px | 403 | 8 |

At 1600 and above the run controls fit on one line and the box is 150px — a clear gain on the 517
the two-box layout managed. At 1280 the re-analyze buttons wrap to their own line and it is
roughly a wash. That is the honest summary: the consolidation is a legibility change that pays
for itself in height only on a wide window.

**`white-space: nowrap` alone made it worse, not better.** It stopped the labels wrapping and the
row overflowed instead, clipping them to "Re-analyze direct (42" and "Re-analyze transitive (6" —
a truncated number on the one control whose entire job is stating the cost before it is spent.
The container needed `flex-wrap` so the buttons move down intact rather than being cut off. Nine
component tests were green throughout; the screenshot showed it immediately.

## The third tier undid it, and `flex-wrap` was why

Adding Owned put a third button in the run-control row, and the box went from 150px to **188px**,
the table start from 441 to 519. `flex-wrap` — the fix above — was the mechanism: **flexbox breaks
lines from each item's content width and only then shrinks what is left on a line**, so the row
wrapped before the sentence beside it ever gave up a pixel. At 1280 CSS px the row wanted
552 (sentence) + 65 + 107 + 113 + 136 + 40 of gaps = 1123 against 982 available.

Two attempts failed and are worth recording because each looked sufficient:

1. **Shortening the labels** to `owned (16)`, with the verb stated once for the row, took 73px
   off. The button tops did not move: `[289, 289, 327]` before and after. 73 was not the 141 the
   line was short by, and nothing part-way helps — the item either fits or it does not.
2. **Making the sentence shrinkable** (`flex: 1 1 auto; min-width: 0`, replacing the `MudSpacer`)
   did not help either, for the ordering reason above. It measured 669px wide, having *grown*
   into the space the wrap freed.

What worked was denying the row the ability to wrap above 1100px, so shrinking became the only
way to resolve the overflow. That needed the `flex-wrap` utility class removed from the element,
because **MudBlazor's utility classes are `!important`** and beat a media query — the rule was
correct and simply never applied, which only `getComputedStyle` revealed. Below 1100px the
sentence has no width left to give, so the row still wraps there; measured at 819 and 879 CSS px
the buttons move down **intact**, never clipped, which is the failure mode the section above
established as the acceptable one.

Verified with `window.innerWidth` read in the same call as every measurement:

| CSS px | run-control row | wrapped | box |
| --- | --- | --- | --- |
| 1600 | 31px | no | 150px |
| 1440 | 31px | no | 150px |
| 1280 | 32px | no | 151px |
| 879 | 70px | yes, intact | — |

Against the 70px row and 188px box the third tier produced, at every width from 1280 up.

**Two measurement traps cost more time than the fix.** `setViewportSize({width: 1600})` yields a
**1280 CSS px** page on this machine — `devicePixelRatio` is 1.25 — so the first round of numbers
above was labeled 1600 and taken at 1280. And the viewport does not hold: one run read
`innerWidth` 1280 immediately before a screenshot and 1536 immediately after, another reported
2412. A screenshot taken at a width that was never set showed the controls simply absent from the
frame, which reads as a far worse bug than the one being fixed. **Read `innerWidth` in the same
`evaluate` as the geometry, and prove "one line" by comparing the items' `top` values** — three
equal tops survive the drift where an image does not. The `rows visible` column earlier in this
document was counted against `innerHeight` 576, not the 720 its heading implies, and is not
comparable to anything measured since.

Nine component tests were green through all of it, again. `ui-test` has no CSS engine, so the
statement it cannot make is the one that matters here.

## Not doing

- **Moving configuration to its own route.** It belongs beside the thing it configures; a reader
  deciding to re-analyze the direct tier wants the queue depth on screen while deciding.
- **Folding the run controls.** They are the answer to "what is it doing", which is why the page
  was opened.
