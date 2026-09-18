# Theme and accessibility

**Status:** both palettes — the graph's and MudBlazor's — are checked as arithmetic by
`PaletteContrastTests`, which runs in `make ui-test` and therefore in `make verify` and CI.

## Why this exists

A queue of 422 items was drawn in `#5C5F66`: byte-identical to `Muted`, the color reserved for a
node that was never a dependency, at 2.69:1 against the dark background and under the 3:1 floor
WCAG 2.2 1.4.11 sets for a graphical object. It said "waiting" and "irrelevant" with one swatch,
and it looked switched off. The comment above the value explained why the choice was deliberate,
which is the case a screenshot review is worst at catching.

Contrast and the palette floors are enforced now — see `.github/instructions/ui.instructions.md`.
What remains is the properties no number covers yet.

## Pending

1. **Nothing checks that a color still means the same thing after a theme toggle.** A key present
   in one scheme and missing in the other is caught; a value *present* in both that reads as a
   different category in one is not. The fork: couple hue across the schemes and assert the
   distance, or name the pairs that must correspond and check only those. It is not obvious the
   schemes should be that tightly coupled — light-mode `queued` is necessarily olive, because a
   yellow bright enough to read as yellow cannot clear 3:1 against white.
2. **Four- and five-item coverage strips can still orphan their last row.** Only 2, 3 and 6 divide
   six, which is why the column counts are stated rather than fitted. The strip renders 4, 5 or 6
   items depending on what is unresolved, and the two short cases are unmeasured.

   **A seventh cell is waiting on this.** The owned figure now reads *"25 · yours, declared of 25"*,
   which says what it counts and stops there. What a reader still cannot see is how much of that
   declaration was ever examined: on the seeded corpus 17 of 25 are analyzed and **6 never can be**,
   and stating the unavailable needs a cell this grid has no room for. Whatever replaces the six
   column counts decides whether that cell can exist.
3. **Component-level layout is checked only by screenshot.** `ui-test` has no CSS engine, and
   every layout defect this plan found was invisible to it and visible in one browser measurement.
   Whether that measurement becomes a build step is `ui/testing.md`'s decision.

## The constraint that must not be broken

**A guard written from a bug report tests the instance; closing the class needs the property
stated independently of what went wrong.** "Clears 3:1" and "is not `Muted`" are both satisfied by
a second grey at 9% saturation. Each pending item above is a property, not a case.

**`Muted` stays at 2.7:1 deliberately.** It means "never a dependency" and is supposed to recede;
its contrast was never the defect, only that a state a reader can act on shared it. Nobody should
"fix" that number later.

## Verification

- `PaletteContrastTests` reports every color that fails rather than stopping at the first, so a
  scheme is never fixed one value per run.
- The screenshot pass in `.github/skills/verify-abom-ui/`, in **both** schemes. Contrast is
  arithmetic; whether a color reads as the thing it names is not.
