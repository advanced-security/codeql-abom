---
description: "Use when changing the codeql-abom interface under ui/ — Razor pages, MudBlazor markup, CSS, the force-graph browser module, or bUnit tests. Covers the three verification layers that are green independently, what the interface must never assert, MudBlazor specificity and flexbox traps, force-graph rules that produce the approved layout, the bUnit harness, and why a reload can still run the previous build."
applyTo: "ui/**"
---

# The ABOM interface

Blazor WASM (.NET 10) + MudBlazor, with the Explorer graph drawn by `force-graph` in
`wwwroot/js/graph.js`. The server marshals `internal/store` structs directly, so their `json:`
tags are the API contract.

## Three verification layers, and green in one says nothing about the others

- `make ui-test` — bUnit component tests in `tests/Abom.WebAssembly.Tests/`. Owns *behavior and
  markup*. It has no CSS engine, so a wrapped label or an overflowing grid passes it.
- `make js` — `node --test` over `wwwroot/js/*.test.js`. Owns the browser module, which no
  compiler reads. A bad edit inside `create()` shows only as a red Blazor error bar at runtime.
- The screenshot pass in the `verify-abom-ui` skill. Owns *layout and appearance*.

`make ui` only compiles Razor. All four are in `make verify`.

## What the interface must not assert

**An absent measurement is not a measurement of zero.** A node ingested since the last reach
rebuild has no `abom_blast_radius` row, and so does a node nothing references. Drawing both at
the smallest radius asserts "nothing depends on this" in a view where size means reach. The
watermark in `abom_graph_generation` is what separates them.

**No verdict the interface derived itself.** Findings are attributed to a CodeQL `rule_id` and
advisories are reported verbatim beside the observed refs. The interface renders evidence and
says what is not known — *not recorded* must stay distinguishable from *none*.

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

The csproj needs `<Compile Remove="tests/**" />`, or the SDK glob compiles the test project into
the app assembly.

## Publishing

CSS and JS are static assets under `wwwroot/`, so they need `make publish`; `make start` does it
for you. **Blazor WASM and ES modules cache hard** — a reload can still run the previous build.
Byte-identical measurements after a real change mean the old build is still being served: confirm
the new markup is present before trusting any number.

Carrying a new field from SQLite all the way to a screen: use the `surface-a-store-fact` skill.
