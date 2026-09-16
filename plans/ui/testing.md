# Interface testing

**Status:** four layers, all four in `make verify`, and the browser suite blocks a pull request
through its own CI job. It resolves its own browser rather than trusting a download. The component
catalog is still an open decision, and deliberately so.

## Why this exists

`make ui` runs `dotnet build`. It proves the Razor parses and asserts nothing about what renders.
Every interface regression this project has produced was invisible to it: a filter that answered
before it asked, controls enabled with nothing to control, a drained queue claiming complete
coverage, a grid overflowing by 288px, six query predicates that parsed cleanly and were ignored.

Four layers, each blind to what the next one sees:

| Layer | Owns | Blind to |
| --- | --- | --- |
| `make ui-test` (bUnit) | Behavior, markup, state transitions | Anything visual — it has no CSS engine |
| `make e2e` (Playwright) | Geometry, boot, the real API contract in a real browser | Whether the result is any good |
| Component catalog *(undecided)* | The appearance of a state no database produces | Regressions nobody looks at |
| `verify-abom-ui` screenshots | Density, color, hierarchy, the invariant audit | Requires a human with working vision |

Green in one says nothing about the others.

## Pending

1. **More populated-corpus assertions.** The suite branches on `corpusSize` and two checks now use
   it — the coverage strip, which orphans a cell only when nothing is declared, and the graph's
   share of the window, which needs a graph to measure. Grids under real labels, wrapping under long
   repository names and the Explorer under load are still unwritten.
2. **Shared fixtures.** A seeded corpus produced by the real pipeline now exists — 25 seed
   repositories, 50 property rows, 1,495 findings, 18 MB as a `.backup` — so the hand-built graph
   fixture that drifts from real ingestion is no longer the only alternative to an empty database.
   What is missing is making it *shared*: checked in, or rebuildable from a recorded input. Until
   then the populated assertions still run only where somebody has crawled an organization, and CI
   reaches the empty half alone. A `.backup` of a WAL database cannot be opened `-readonly` until
   its journal mode is reset, which is worth knowing before it becomes a fixture.
3. **A corpus that can exercise a shared owned dependency.** The crawled organization holds **no
   dependency edge from one owned repository to another distinct one**, so nothing in it can show
   that excluding one repository grouping leaves a repository another grouping still reaches. The 5
   edges that match the obvious query are repositories using their own composite action, which is
   worth stating because the next person to measure it will find 5. Only a Go fixture covers it.
4. **Decide the component catalog.** Deferred rather than answered: Playwright covers geometry and
   integration, which was the row with no owner, and a catalog answers a different question — the
   appearance of a state no database produces. The alternatives stay named: **BlazingStory**,
   Blazor-native with no npm, whose own README calls it a personal hobby project; **a dev-only
   `/stories` route**, no new dependency and the chrome is ours to build; **neither**, with seeded
   fixture databases. Three entries in the capture matrix still need a specific database — "nothing
   started", "no advisories", "server unreachable".

## The constraint that must not be broken

**The cost is the decision, and it is now measured.** The harness adds **four npm packages**
(`@playwright/test`, `playwright`, `playwright-core`, `fsevents`) and one **130 MiB** browser, and
it is the only npm dependency in a repository whose subject is supply-chain risk. It is scoped to
`ui/tests/e2e` so nothing it pulls in can reach a shipped artifact, the version is pinned exactly,
and the lock file is checked in because it is the only record of what actually resolved. Paying
that deliberately is defensible; paying it because somebody needed one measurement is not.

**5xx means ABOM is broken; 4xx means the reader has not done something yet.** The suite fails on
the first and not on the second. Banning every console error instead would make correct first-run
behavior — asking for a bill of materials before declaring a scope — indistinguishable from a fault.

**A known defect is recorded, never silenced.** `test.fail()` marks one that is expected to fail,
so it reports, and so the run breaks when somebody fixes it. A skip reports as a pass. **Mark it
only where it actually fails**: an unconditional `test.fail()` on a defect that only appears with
an empty corpus made every populated run report a failure, so the suite could never be green
against real data — which is the run that has to gate.

**What bUnit can state without a browser, it should.** A layout constraint expressed as a DOM count
needs no CSS engine, and the browser suite is minutes where bUnit is seconds. The same goes for
arithmetic: the graph's height and the browser choice are pure functions under `make js`, which
needs neither a browser nor a server.

## Verification

- `make e2e` against a fresh database, in about a minute. Five consecutive runs clean — three
  empty, two populated — which is the bar for a gate rather than a report.
- For any new test: break the production code on purpose, watch it fail, restore.
- For any geometry claim, read `window.innerWidth` in the same call as the measurement — a
  requested 1920 can be reported as something else, and a media query that never matched looks
  exactly like a dead layout.
- `make e2e` refuses to run against `ABOM_DB`, so a suite that writes cannot reach the working
  corpus.
