---
description: "Use when changing the codeql-abom interface under ui/ — Razor pages, MudBlazor markup, CSS, the force-graph browser module, or bUnit tests. Covers the three verification layers that are green independently, what the interface must never assert, MudBlazor specificity and flexbox traps, force-graph rules that produce the approved layout, the bUnit harness, and why a reload can still run the previous build."
applyTo: "ui/**"
---

# The ABOM interface

**Fixed file.** Do not edit or extend it. Work goes in `plans/**`, what the system does in
`docs/*.md`, a lasting lesson in `.github/skills/**`. A rule here that is wrong: say so and stop.

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

**No count tiles.** A box holding a big number and a caption, linking nowhere, repeats a figure a
section below already carries and pushes the first useful row off the screen. Put the figure in
the sentence or column it qualifies. `check-style.mjs` rejects heading-sized figures; a card whose
figure is itself the way into a scope says so in a `style-check: off` block.

**A page does not keep its own list of query parameter names.** The address is read with the same
`ViewSpec`/`ScopeService` code the API and the gate read it with. The Explorer kept thirteen
`[SupplyParameterFromQuery]` attributes beside the shared serializer and the two lists drifted: it
wrote `ref` and read `refStyle`, so the reference filter was reset on every reload, and `scope` and
`edges` were written and never read at all. A term a surface has no control for is carried through
untouched rather than dropped — widening somebody else's question in passing is the worse failure.

**The interface names a custom property, never what it might mean.** ABOM is told a key. An
organization running `business_id` gets business units and one running `team` gets teams; which it
is, is not something ABOM knows. Render the key and the value verbatim — MudBlazor uppercases button
labels, and `BUSINESS_ID` is not the string the operator matches against GitHub's own settings.

**A scope narrowed by a property says who it left out.** `PropertyBreakdown.WithoutValue` counts the
owned repositories carrying no value for that key; they vanish from every filtered answer, and a
reader who is not told believes they scoped when they truncated. It belongs beside the filtered
total, not behind a tooltip. The bucket a repository lands in for doing nothing is marked as the
organization's default, or the largest group reads as a decision nobody made.

**An empty state is a stage, not an apology.** Before anything is declared the Explorer says so and
points at Settings; after a declaration and before enumeration finishes, it says what it is waiting
for. "None declared" is not "0".

**A filter the view is not applying may not look as though it is.** Naming a node suspends the
arrival-scope flags, because otherwise `has findings` deleted the neighborhood the reader had just
asked to see — 23 nodes and 22 edges drawn as 1 and 0. The suspension is named on screen beside the
focus, for the same reason every other exclusion is.

**A reader is never told that unlabeled means unimportant.** Whatever rule decides which nodes carry
text, the ones without it must not read as background. An absent label is a rendering budget, not a
judgement about the node. The same goes for a boundary: one drawn with nothing saying whose it is
leaves a reader counting shapes and names and finding different numbers.

**Scope is the fix, not styling.** When a force view cannot be read, the answer is fewer nodes —
coarser granularity, fewer hops, a tighter filter — never a prettier layout over the same set. A
denser view that has been restyled is the same view; it just takes longer to discover that.

**Shape stays a channel beside color.** Node kinds are distinguishable without hue today. A redesign
that reaches for color alone fails for roughly one in twelve men, and passes every automated check
we have.

**A collapsed section still states what it is set to.** A fold that hides a binding turns it into
something a reader has to go looking for, and a reader who does not know to look assumes the
default.

**A default that scopes and filters has to report both exclusions.** The Explorer's arrival hides
what is outside the closure and what inside it has no finding; a counts sentence carrying one number
asserts a clean estate for everything it dropped, in the grouped branch as much as the ungrouped.
**A dropped relationship counts as an exclusion** — 39 of 7,552 is not a drawing bug and must not
look like one.

**Legibility is a property of scope, not of layout.** Drawn over everything the crawl walked
through, the graph is a starburst in which no edge can be followed end to end, and the boundary
classification paints it one color because almost every edge crosses accounts. A classification is
only legible inside a scope small enough for the distinction to vary. The answer to an unreadable
force view is fewer nodes, never a prettier layout over the same set.

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
- **A disabled `MudToggleItem` suppresses pointer events on its children**, so a tooltip inside one
  never fires — and the tooltip a reader needs most is the one saying why the control is disabled.
  Put it on a span outside the disabled item.
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
- **A flat view has nothing to rotate, and Trackball matches on the button number.** Its handler is
  `switch (event.button) { case mouseButtons.LEFT: state = ROTATE; … }`, so the action is fixed per
  case: setting `LEFT = PAN` makes the left button match nothing, which measures exactly like the
  bug. Swap `LEFT` and `RIGHT` instead. In 2D a left drag otherwise moves the camera **0** units
  while the canvas hint promises it pans.
- **`zoomToFit` runs on `onEngineStop`**, not after `setData`: fitting seeded positions frames a
  cluster the simulation is about to expand.
- Key listeners belong on `document` — the WebGL canvas never holds focus — and **must** be
  removed in `dispose()`, or a stale page answers keys for the live one. The same goes for every
  listener bound to the host: the Explorer rebuilds the view on each filter change, and a leaked
  `dblclick` meant N handlers each zooming 0.6×, which compounds to 0.6ⁿ and flies the camera out
  of the scene. `create.test.js` asserts each one is gone after `dispose`.
- **A view at rest is not listening.** `drawLabels` returns without rescheduling once `restLevel`
  reaches `still`, and `pauseAnimation()` has stopped the renderer, so nothing polls the camera and
  a drag moves a camera whose frame nobody draws — hover cannot rescue it either, because
  force-graph raycasts inside the paused loop. Any pointer gesture must `wake()`. Measured: the
  same drag changed **97% of pixels on a fresh view and 0% once settled**.
- **`nodeVisibility` governs the scene, not the overlay.** `drawLabels` projects from
  `graphData()` directly, so a node hidden from the renderer keeps its label until the projection
  filters it too — which showed a boundary named `github · 46` beside a ghost label for a point no
  longer drawn.
- The dense-blob layout is an approved design claim, not an accident. Do not change
  `distanceMax(Infinity)`, the degree-weighted gravity, `fitCore`'s 90th-percentile framing,
  `linkWidth(0)` or hover-only labels without measuring the result.

## Boundaries, and what a filter may remove

**A region asserts containment, so it may not enclose a non-member.** A convex hull over a C-shaped
cluster swallows nodes belonging to nobody in it, which states a relationship that does not exist.
A boundary that cannot be drawn honestly is **withheld and counted**, never redrawn smaller.
**Splitting such a group is refuted, not untried**: one hull per group gave 23 boundaries, 71
foreign nodes enclosed and 20.7% overlap; splitting at the widest gap gave **82 / 197 / 532.7%**.
The invariant is enforced by construction — an iteration that gave up after a fixed number of
passes returned shapes inconsistent with the containers it then painted.

**Boundaries and relationships cannot both survive a filter, and `Group by` is where the reader
chooses.** An edge needs both of its ends, so removing what a filter did not match removes the
relationships too — `has findings` is on by default and left **39 of 7,552**. A boundary drawn
around only the nodes that matched claims the group is *only* those, so a grouped view must still
remove; ungrouped there is no such claim and nothing has to go, so what fails is greyed instead.
Each option states its half of the trade in its own tooltip. Applying both at once was measured and
rejected: boundaries fell to 18 of 123, because the greyed nodes interleave with every group.

**A boundary's name is placed against the canvas, not just against the hull.** Placement that had
never been told the canvas size drew four of 24 names off its edge, including the largest account in
the corpus and the operator's own. Clamping then makes the candidate ladder degenerate — against an
edge every rung lands on the same clamped row, and the caption owns that strip — so candidates must
go above, below *and* to either side. Whatever still cannot be placed is counted and disclosed.

**The focus ring is the contract for every gesture.** A node is drawn at about half a pixel, so the
renderer's hit test almost never fires: it left right-click isolate unreachable and made
double-click depend on whether two presses happened to agree. `click`, `dblclick`, `pointerup` and
`contextmenu` are owned on the host and resolve through the nearest-node pick the ring shows.
**Do not rely on `clickAfterDrag`** — it defaults to false, by a heuristic that cannot be seen,
tuned or tested, and it silently dropped clicks carrying two pixels of trackpad drift. Decide
click-versus-drag from the press-to-release distance. Right-click isolates on `pointerup`, never on
`contextmenu`, which fires on the press and would isolate on every right-drag pan.

## Coverage is a ratio, and only a fold can be part-way

**The reference split is what makes a boolean the right answer, expanded.** A dependency the queue
fetched at several references is drawn as one node per reference, so each carries exactly one: 955
of 955 nodes on the working corpus are all-or-nothing, and a node's coverage cannot be "1 of 27".
Seeing which references are covered beats a ratio that hides them.

**A fold sums them, and that is where a queue state stops being enough.** A collapsed group carries
`refsInUse`/`refsAnalyzed` and **no `discovery` state at all**, so coloring the Coverage view by
state painted every group muted — which that legend reads as "never a dependency". Measured: 126 of
126 groups muted, 43 of them partly analyzed (`actions` 216/259, `github` 94/122). `coverageColor`
blends by share and keeps the state only where there are no references to count, because "not a
dependency" is a fact about a workflow under review and must not read as "we have not looked yet".

**The Reference filter and the coverage claim are different questions, and the control says so.**
It selects nodes by how something references them; coverage is about the reference each was
analyzed at. 215 of 2,639 nodes are referenced both by tag and by commit, so filtering to one does
not narrow what Coverage claims about them. Both facts are true; the tooltip is what stops a reader
reading one as the other.

## Measuring a canvas

- **`readPixels` returns all zeros after compositing** unless the context was created with
  `preserveDrawingBuffer`. Every "the canvas is empty" reading taken that way is the instrument
  lying, and it cost several rounds of chasing a defect that did not exist.
- **Validate the instrument before believing it about the subject.** Take the same measurement
  across a change you *know* redraws — toggling `Collapse` moved 96% of pixels — and only then
  trust a zero.
- **`setViewportSize` is not `innerHeight`.** Asking for 900 yielded 600, so a height computed from
  the window looked like it was collapsing when it was correctly hitting its floor. Read
  `window.innerHeight` rather than assuming the size you requested.
- **A stale published bundle looks exactly like a bug.** `make restart` republishes; open the page
  with `forceNew` rather than reloading, or the browser answers from the build before the fix.
- **A 2D context is one piece of shared state, and `textAlign` is part of it.** A note drawn
  left-aligned before the node-label loop left every label offset from its own box. Restore what you
  change, or set it at the top of each pass; nothing in `make verify` can see this.
- **Synthetic pointer events cannot hit a target smaller than a pixel.** A 13×7 grid of
  `mouse.move` positions found no node, which read as "hover is broken"; the node's drawn radius was
  **0.64px**. Zoom the camera to a single node before concluding the hover path is wrong.
- **Measure density before designing a shape over it.** Nearest-neighbour spacing decides whether
  any boundary, hull or halo can be honest. At 2px median spacing with 26px padding, every shape
  encloses its neighbours and no partition exists.
- **A metric moving 75x in the intended direction is not evidence the change was right.** Restoring
  the missing edges took drawn relationships from 39 to 2,936 and *improved* node spacing, and the
  screenshot showed the saturated starburst the scope exists to avoid, with honest grouping falling
  from 24 boundaries to 14. Count what you are optimising, then look at the picture; only the second
  one can tell you that you swapped one failure mode for its opposite.
- **Nothing in a zooming canvas may be sized in screen pixels alone.** A fixed gap is a different
  thing at every zoom — boundary padding of 26px left the members a speck inside a shape that was
  almost all margin. Scale with what is enclosed, with a floor and a ceiling.
- **A per-frame answer that depends on itself has to reach its fixed point inside the frame.**
  Refusing a boundary puts its container back, which the next boundary must clear; decided one frame
  at a time it drew 9 of 17 and a view at rest never finished. Iterate, then paint.
- **`page.waitForFunction` with an `async` predicate resolves immediately** — the promise is truthy,
  so `jsonValue()` is `null` and the condition was never waited for. Use `expect.poll`. Neither
  `test.setTimeout` nor the `{ timeout }` details argument changed the reported budget here, so keep
  a poll inside the config's own timeout, where a failure names what did not happen.

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
