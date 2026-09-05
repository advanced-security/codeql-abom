# Theme and accessibility

**Status:** the palette is checked as arithmetic rather than judged by eye. Layout is not.

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

`Muted` is deliberately left at 2.7:1. It means "never a dependency" and is *supposed* to recede;
the defect was never its contrast, only that another state shared it.

## The constraint that must not be broken

**A state a reader can act on may not wear the colour of something that was never measured.**
This is the same rule the graph already follows for size — an unmeasured node is not drawn at the
smallest radius — applied to hue. `queued` and `Muted` being one colour said "waiting" and
"irrelevant" with one swatch.

**Colour is checked as numbers.** Every value is one source in `ThemeService`, so a test can read
them all; nothing may reintroduce a literal colour elsewhere, which `make style` already enforces.

## Pending

1. **The MudBlazor semantic palette is unaudited.** `PaletteContrastTests` covers the graph
   palette. Chips, buttons and alerts read their colours from `MudTheme`, which no test measures.
2. **Layout is unaudited, and layout is where the remaining defects are.** Known, from
   screenshots: the coverage strip wraps its last item onto a line of its own at 1440px, and the
   two re-analyze controls wrap to three lines each in the Discovery header.
3. **Nothing checks the two schemes render the same information.** A value can pass contrast in
   both and still mean different things, and only dark is usually looked at.

## Verification

- `PaletteContrastTests`, in `make ui-test` and therefore in `make verify` and CI.
- The screenshot pass in `.github/skills/verify-abom-ui/`, in **both** schemes. Contrast is
  arithmetic; whether a colour reads as the thing it names is not.
