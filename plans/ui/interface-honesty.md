# The interface asserts things in ways a reader cannot check

**Status:** not built. A UI audit found four classes of defect that are independent of any
restructuring: a control with no name, an encoding only some readers can see, a figure that reads
as a different measurement than it is, and machine text on a user's screen. Re-measuring from the
DOM found the first at six times the size first written down, and the second with no second channel
to repair.

## Why this exists

None of these is a layout preference. Each one makes the interface say something it cannot support,
or say it to only some of the people reading it — which is the same failure the architectural
invariants exist to prevent, arriving through the presentation rather than through the data.

They are collected here rather than spread across the surfaces they appear on, because they are one
kind of work: making an existing claim checkable. The structural work is
[`inventory-units.md`](inventory-units.md) and does not depend on any of this.

## Pending

1. **Fifty-six controls have no accessible name, and the Explorer holds most of them.** Measured
   from the DOM across every surface: a control is counted only when it is visible, focusable, and
   carries no text, `aria-label`, `aria-labelledby`, `title` or `placeholder`. The Dashboard is
   clean at **0**. The Explorer has **45** — **21 checkbox inputs**, **12 colour swatches**, the
   seven `data-abom-property-exclude` buttons from `ScopeControl`, 3 icon buttons and 2 text inputs.
   Inventory has **8**, Settings **2** (the per-account *On* and *only these repositories*
   checkboxes), the bill of materials **1**. Their only label is a `MudTooltip`, which is a hover
   popover, so a keyboard or screen-reader user meets them unnamed. The test hook
   `data-abom-property-exclude` is not a name. The Explorer is where the work is, and it was the
   surface nobody measured. Depends on nothing.
2. **The owned figure reads as coverage and is not one.** The strip renders *"25 yours of 25"*,
   which is `ownedRepos` of `seedRepos` — a fact about what was declared. Beside *"upstream
   analyzed"* it reads as a fact about what was examined, and on this corpus those are very
   different: at depth 0 the frontier holds **17 done, 2 pending, 6 unavailable**, so **8 of 25
   declared repositories have never been analyzed and 6 never will be**. It no longer renders on the
   Dashboard, which was the surface where the misreading was worst; it still renders on **four**
   others — Inventory, the Explorer, Settings and the bill of materials. Either the figure says what
   it counts, or it states analyzed-of-declared and the strip gains the unavailable. **Interacts
   with [`theme.md`](theme.md)**, whose grid is tuned for six cells and is pinned by
   `CoverageStripTests.TheStripStaysWithinTheSixCellsItsGridIsTunedFor` — a seventh cell is that
   plan's work, not this one's. Depends on nothing.
3. **Kind is encoded by hue and by nothing else.** There is no shape channel: `ThemeService` maps
   each kind to one colour and says so in its own comment — *"Kind is the first thing a reader wants
   to tell apart, so it is what color encodes"* — and the canvas draws every node as a circle. The
   two commonest kinds in the corpus are `action` `#FF6B6B` and `workflow` `#63E6BE`, red against
   green, which is the one pairing to design around. `job` `#A9B1BA` and `step` `#868E96` are two
   greys a shade apart in dark, `#6C757D` against `#868E96` in light — near-indistinguishable rather
   than literally identical, which is worse, because a reader who can see a difference cannot tell
   which is which. A second channel has to be introduced, not repaired. Depends on nothing.
4. **Machine text reaches the reader.** A refused term renders as
   `view: this answer applies no "boundary"` — the `view:` prefix is a Go error namespace. The
   refusal also names the term without naming the surface that *does* answer it, which is the one
   thing that would turn a dead end into a redirect; the catalog already computes this per view as
   `listingRefuses`, so the ad-hoc refusal is the remaining case. Alongside it: *"1 nodes"* and
   *"1 repos"* are not pluralized. Depends on nothing.
5. **The force-directed graph has never been verified by looking at it.** Every check in the
   `verify-abom-ui` skill that needs a settled layout — hairball, hub occlusion, whether position
   encodes urgency legibly, view-switch continuity, determinism across a reload — is **untested**.
   The audit could not run them: the browser page was backgrounded, `document.visibilityState` was
   `hidden`, and it recorded **0 animation frames in 2 seconds**, so the force simulation could not
   advance and two captures 90 seconds apart were pixel-identical. This is a harness problem, not an
   application one, and it means the graph's visual claims are unverified rather than verified-good.
   What is owed is a way to run those checks in a foreground browser. Depends on nothing.

## The constraints that must not be broken

**An encoding that needs colour needs a second channel.** Severity already pairs its dot with a word
and passes; kind has no second channel at all. Shape, label or position — but not hue alone, and not
hue plus a shape every kind shares.

**A control conveys its purpose without hovering.** A tooltip is an enhancement. If the tooltip is
the only label, the control is unnamed for anyone not using a mouse.

**A figure is labelled with what it counts.** Not with what a reader on that screen probably wants
to know, which is how a declaration came to sit under a coverage heading.

**A refusal is a redirect.** Naming the term is the floor. Naming the surface that answers it is the
point, because a reader who has been refused is a reader who asked a reasonable question in the
wrong place.

## Verification

- The exclude controls have an accessible name, asserted from the DOM rather than from the markup,
  and the whole scope bar is operable from the keyboard alone.
- The kind legend is legible in a greyscale rendering of the screenshot: every kind still
  distinguishable with hue removed.
- No user-facing string contains `view:`, `frontier`, `corpus` or a bare unpluralized count, checked
  by the interface style gate rather than by reading.
- The force-graph checks in `verify-abom-ui` actually run, against a populated corpus, in a browser
  that is in the foreground — and report a verdict instead of "blocked".
