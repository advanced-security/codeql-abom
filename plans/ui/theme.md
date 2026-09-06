# Theme and accessibility

**Status:** shipped. Both palettes are checked as arithmetic — the graph's and MudBlazor's — and
the two layout defects this plan named are measured and fixed. What is still unchecked is stated
under Pending, and it is narrower than it was.

## Why this exists

"Queued" was reported as looking like an unfilled or disabled control on the Discovery page in
dark mode. It was worse than a matter of taste. The colour was `#5C5F66`, **byte-identical to
`Muted`** — the colour reserved for a node that was never a dependency — and it sat at **2.69:1**
against the dark background, under the 3:1 floor WCAG 2.2 1.4.11 sets for a graphical object. So
a queue of 422 items was drawn in the colour that means "this is not part of the story", at a
contrast that made it look switched off. Both readings were wrong and both were invisible to
every check the project had.

The comment above it said the choice was deliberate: *"Queued is deliberately the muted color …
'not looked at yet' is exactly what grey should say."* The reasoning was sound and the result was
not, which is the case a screenshot review is worst at catching.

## What the audit found

`PaletteContrastTests` computes WCAG relative luminance for every palette entry against its own
background, in both schemes. On first run it failed six ways across four defects:

| Defect | Measured |
| --- | --- |
| `queued` on dark | 2.69:1, below 3:1 |
| `queued` on light | 2.07:1, below 3:1 |
| `queued` equals `Muted` | identical string, both schemes |
| `org` kind on light | 2.48:1, below 3:1 |
| `Focus` on light | 2.48:1, below 3:1 |

The last was not part of the report and is the most serious: **`Focus` is the selection colour**,
the one thing on screen that has to win, and in light mode selecting a node changed it by almost
nothing.

## What changed

- `queued` → `#8A929E` on dark (6.0:1) and `#5F6B7A` on light (5.5:1). A slate that is plainly a
  colour, without competing with the states a reader acts on.
- `org` and `Focus` on light → `#B35C00` (4.7:1). They were already the same value and stay so;
  darkening both preserves whatever relationship the original chose.

**The queued value did not survive, and the reason is the useful part.** It shipped, passed every
check here, and was still grey — about 9% saturation. Both assertions this audit added describe
the defect that was *found* rather than the property that was *wanted*: "clears 3:1" and "is not
`Muted`" are things a second grey passes comfortably. Nothing in the suite could tell a hue from a
grey. `queued` is now `#F5D90A` on dark and `#9A7B00` on light, behind a chroma floor and a
hue-distance floor added in `ui/tier-suites.md`.

The lesson generalizes past colour: **a guard written from a bug report tests the instance, and
closing the class needs the property stated independently of what went wrong.**

`Muted` is deliberately left at 2.7:1. It means "never a dependency" and is *supposed* to recede;
the defect was never its contrast, only that another state shared it.

## The constraint that must not be broken

**A state a reader can act on may not wear the colour of something that was never measured.**
This is the same rule the graph already follows for size — an unmeasured node is not drawn at the
smallest radius — applied to hue. `queued` and `Muted` being one colour said "waiting" and
"irrelevant" with one swatch.

**Colour is checked as numbers.** Every value is one source in `ThemeService`, so a test can read
them all; nothing may reintroduce a literal colour elsewhere, which `make style` already enforces.

## The second pass: the palette nobody was measuring

The audit above measured `GraphPalette` to two decimals while `MudTheme` — which every chip,
alert, button, progress bar and icon in the interface reads from — was measured not at all. The
graph is the part that cannot inherit CSS, so it got the attention; the part that *can* inherit it
was assumed to be handled, and MudBlazor does not check contrast either.

**Two light values were below the text bar, and why they count as text is the whole finding.**
MudBlazor's outlined chip draws its label and border in the semantic colour on the page
background, and this interface uses exactly that for "build-time only" and for every pinning
level. So `Success` and `Warning` are read, not merely seen, and answer to 4.5:1 rather than 3:1:

| | measured | now |
| --- | --- | --- |
| light `Success` `#2F9E44` | 3.45:1 | `#2A833B`, 4.76:1 |
| light `Warning` `#E8590C` | 3.58:1 | `#C25100`, 4.70:1 |

Dark was fine throughout, at 6.24:1 to 8.59:1 — the pattern this plan keeps finding: **the scheme
that gets looked at is the one that is already right.**

These two constants are also the light `done` and `retrying` discovery states, so darkening them
had to survive the chroma and hue floors added earlier. It does, and the hue distance between
`retrying` and `unavailable` improves from 21° to 25° against a 20° floor — it had been sitting
one degree above failing.

**The test reports every colour rather than the first that fails.** The first version stopped at
`Success`; `Warning` would have surfaced only on the next run, one fix at a time, hiding that half
the light scheme was involved.

## The second pass: the strip that orphaned its last item

Six coverage items packed greedily left the sixth alone on a line of its own. The instinct is to
recover width; the measurement says width was never available. At 1152 CSS px the items want
**1,062px against 880px** — all five gaps together are worth 100px, so no gap, font or padding
change puts six on one line there. The defect was never the wrap. It was the orphan.

Nor could the labels be shortened: "dependencies unresolved" and "findings not in the Explorer"
are the two items that say what is *not* known, which is a stated invariant rather than decoration.

So the column count is **stated rather than fitted**. `repeat(auto-fit, minmax(…))` was tried
first and is what produces the orphan — it takes whatever fits, and at 1440 that is five columns
for six items. Only 2, 3 and 6 divide six:

| CSS px | before | after | height |
| --- | --- | --- | --- |
| 1600 | 5 + 1 | **6** | 70px → 32px |
| 1440 | 5 + 1 | **3 + 3** | 70px → 56px |
| 1152 | 5 + 1 | **3 + 3** | 70px → 56px |
| 800 | 4 + 2 | **2 + 2 + 2** | 81px |

Nothing is clipped at any width, which is the failure the earlier `white-space: nowrap` attempt in
`discovery-config.md` produced, and the reason width is never recovered by truncation here.

## Pending

1. **Nothing checks that a colour still means the same thing after a theme toggle.**
   `BothSchemesDescribeTheSameSetOfThings` now catches a key present in one scheme and missing in
   the other, which is the failure that would blank a swatch. It does not catch a value that is
   *present* in both and reads as a different category in one — that needs a hue correspondence
   between the schemes, and it is not obvious the two should be that tightly coupled.
2. **Four- and five-item coverage strips can still leave a short last row.** Only 2, 3 and 6
   divide six, and the strip renders 4, 5 or 6 items depending on what is unresolved. Six is the
   case that occurs while discovery has work left, so it is the one the column counts are chosen
   for.
3. **Component-level layout is still only checked by screenshot.** `ui-test` has no CSS engine.
   Every defect this plan found was invisible to it and visible in one measurement.

## Verification

- `PaletteContrastTests`, in `make ui-test` and therefore in `make verify` and CI.
- The screenshot pass in `.github/skills/verify-abom-ui/`, in **both** schemes. Contrast is
  arithmetic; whether a colour reads as the thing it names is not.
