---
description: "Use when changing the codeql-abom interface under ui/ — Razor pages, MudBlazor markup, CSS, the force-graph browser module, or bUnit tests. Covers the three verification layers that are green independently, what the interface must never assert, MudBlazor specificity and flexbox traps, force-graph rules that produce the approved layout, the bUnit harness, and why a reload can still run the previous build."
applyTo: "ui/**"
---

# The ABOM interface

Blazor WASM (.NET 10) + MudBlazor, with the Explorer graph drawn by `force-graph` in
`wwwroot/js/graph.js`. The server marshals `internal/store` structs directly, so their `json:`
tags are the API contract.

## Four verification layers, and green in one says nothing about the others

- `make ui-test` — bUnit component tests in `tests/Abom.WebAssembly.Tests/`. Owns *behavior and
  markup*. It has no CSS engine, so a wrapped label or an overflowing grid passes it.
- `make js` — `node --test` over `wwwroot/js/*.test.js`. Owns the browser module, which no
  compiler reads. A bad edit inside `create()` shows only as a red Blazor error bar at runtime.
- `make e2e` — Playwright in `tests/e2e/`, driving the published interface in a real browser.
  Owns *geometry*, the boot path and the API contract as the page actually exercises it. **In
  `verify`**, and blocking: it resolves its own browser through `tests/e2e/browser.mjs`, which
  prefers the pinned build, falls back to system Chrome when that build is missing *or
  half-extracted*, and refuses outright when there is neither. Checking the launcher alone is not
  enough — a stalled download leaves it in place without the framework beside it, and every test
  then dies in `dlopen` rather than saying "no browser". `ABOM_E2E_DB=<copy>` points it at a real
  corpus; `ABOM_E2E_CHANNEL` overrides the choice and is never second-guessed.
- The screenshot pass in the `verify-abom-ui` skill. Owns *appearance* and taste.

`make ui` only compiles Razor. All four are in `make verify`.

**5xx means ABOM is broken; 4xx means the reader has not done something yet.** The browser suite
fails on the first and not the second, and handlers must keep that line: a precondition the reader
has not met is `writeError(w, http.StatusBadRequest, ...)`, never a 500. Reported as a fault it
tells a reader the server is broken and leaves the page unable to name what would fix it.

## What the interface must not assert

**An absent measurement is not a measurement of zero.** A node ingested since the last reach
rebuild has no `abom_blast_radius` row, and so does a node nothing references. Drawing both at
the smallest radius asserts "nothing depends on this" in a view where size means reach. The
watermark in `abom_graph_generation` is what separates them.

**No verdict the interface derived itself.** Findings are attributed to a CodeQL `rule_id` and
advisories are reported verbatim beside the observed refs. The interface renders evidence and
says what is not known — *not recorded* must stay distinguishable from *none*.

**An empty result is not the same as an empty answer.** The boundary filter returned nothing
because no cycle had classified the edges yet, and the page said only "no results" —
indistinguishable from a filter that ran and matched none. Where a view can be empty because
something has not run, say which, and name the action that would change it.

**A default that hides things must say so on screen.** "Showing 213 of 401 — 188 hidden by
filters" is the floor. A default that quietly drops everything without a finding asserts a clean
estate.

**An empty state is a stage, not an apology.** Before anything is declared the Explorer says so and
points at Settings; after a declaration and before enumeration finishes, it says what it is waiting
for. "None declared" is not "0".

**A collapsed section still states what it is set to.** A fold that hides a binding turns it into
something a reader has to go looking for, and a reader who does not know to look assumes the
default.

**One vocabulary.** The words for ownership, boundaries, exposure and coverage are fixed in
[`docs/vocabulary.md`](../../docs/vocabulary.md) and enforced by `checkVocabulary` in
`ui/tools/check-style.mjs`, which fails the build on the retired spellings.

## Every open view refreshes itself

There is no Follow checkbox; `FollowService` is only the beat
(`localStorage['abom.follow']`, seconds). The checkbox was disabled whenever discovery was
stopped, so a page could not watch for a state it was on its way *out of* — "Pausing…" survived
until a manual reload. `InitializeAsync` must fire `Changed`: the layout restores the interval
from `OnAfterRenderAsync`, after pages have already armed timers on the default.

## MudBlazor

- **Its selectors outrank single-class overrides.** `.abom-grid td` loses to
  `.mud-table-dense .mud-table-cell`; use `.abom-grid .mud-table-root .mud-table-cell`.
- **Its utility classes are `!important`.** A media query on `.abom-run-row { flex-wrap: nowrap }`
  silently lost to the `flex-wrap` utility in the element's class list. Remove the utility and own
  the property in CSS. Computed style is the only way to tell.
- **`table-layout: fixed` breaks MudDataGrid** — header and body widths decouple and columns
  overlap. Trim cell padding to recover width instead.
- **Flexbox wraps before it shrinks.** Line breaking uses each item's *content* width, so
  `flex: 1 1 auto; min-width: 0` on a neighbour cannot stop a sibling wrapping — the neighbour
  grows into the space the wrap just freed. Set `flex-wrap: nowrap` with `flex-shrink: 0` on what
  must not squash.
- **An expansion panel renders its content whether or not it is expanded**, so a markup assertion
  proves nothing about what a reader can see.
- **`Immediate="true"` binds to `oninput`**, while bUnit's `Change()` raises `change`. The input is
  silently ignored and the test passes for the wrong reason.
- **A component parameter needs `@` to be an expression.** `Value="_search"` passes the literal
  string `_search`; only `Value="@_search"` passes the field. Every page using the shared search box
  did this, so each grid filtered correctly on the C# value while the control above it displayed the
  variable's name — the one place a reader looks to find out what they are filtered by. No data or
  CSS check can see it; assert what the control renders.

## Color is checked as arithmetic

Every value has one source in `ThemeService`, so a test can read them all; nothing may reintroduce
a literal color elsewhere. Two floors are enforced: a **chroma floor**, because a color carrying a
category must not be greyscale, and a **hue-distance floor** within a single legend.

**A state a reader can act on may not wear the color of something that was never measured.**
`queued` and `Muted` sharing one swatch said "waiting" and "irrelevant" with the same ink.

## force-graph

- **One labelling system.** `.nodeLabel(() => '')` is deliberate — force-graph's tooltip and our
  canvas `drawLabels` both ran, labelling every node twice, and the tooltip kept naming the last
  node it hovered because it knows nothing about selection. Do not "restore" it.
- **Selection has two independent halves**: `_isolated`/`_selected` in C# and `it.selected` in JS.
  Clearing C# alone leaves the dimming on; call `setSelection(id, [])` too.
- **Explicit `.width()`/`.height()`**, or it defaults to the window and draws outside its
  container. Measure the host rather than `calc(100vh - Npx)`, and measure again on data load.
- **`zoomToFit` runs on `onEngineStop`**, not after `setData`: fitting seeded positions frames a
  cluster the simulation is about to expand.
- Key listeners belong on `document` — the WebGL canvas never holds focus — and **must** be
  removed in `dispose()`, or a stale page answers keys for the live one.
- The dense-blob layout is an approved design claim, not an accident. Do not change
  `distanceMax(Infinity)`, the degree-weighted gravity, `fitCore`'s 90th-percentile framing,
  `linkWidth(0)` or hover-only labels without measuring the result.

## Tests

`AbomTestContext` mirrors `Program.cs` DI rather than inventing a convenient set: `JSInterop.Mode
= Loose` because MudBlazor touches the DOM constantly, and HTTP is faked at `HttpMessageHandler`
so the server's JSON contract stays under test. `Withhold(path)` holds a response open, because
the interesting bug window is *while a request is in flight*.

In JS fakes, `requestAnimationFrame` must **not** invoke its callback: `drawLabels` re-arms itself
every frame and a naive stub recurses until the stack dies.

In the browser suite, wait for `.abom-boot` to go rather than for `networkidle`. Every open view
re-polls on a timer, so "no requests for 500ms" is a property of the poll interval; asserting while
the static boot shell is still up measures the boot screen and every content check fails for the
same misleading reason. A known defect is recorded with `test.fail()`, never `skip` — a skip reports
as a pass, and `test.fail()` breaks the run when somebody fixes the thing.

**A measurement taken in a browser must prove the thing it timed actually moved.** A frame-rate
probe that clears the rest state without calling `graph.resumeAnimation()` counts the browser's
idle loop and reports a flat 60 FPS everywhere. Ask a node where it was before and after; `moved: 0`
is the tell. Node count is the cost driver — with every edge removed the ceiling is under 5 FPS at
corpus scale — so the simulation set, not the renderer, is what has to stay small.

The csproj needs `<Compile Remove="tests/**" />`, or the SDK glob compiles the test project into
the app assembly.

## Publishing

CSS and JS are static assets under `wwwroot/`, so they need `make publish`; `make start` does it
for you. **Blazor WASM and ES modules cache hard** — a reload can still run the previous build.
Byte-identical measurements after a real change mean the old build is still being served: confirm
the new markup is present before trusting any number.

Carrying a new field from SQLite all the way to a screen: use the `surface-a-store-fact` skill.
