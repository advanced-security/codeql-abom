# Interface testing

**Status:** four layers, three of them in `make verify`. Browser measurement is now a suite —
Playwright under `ui/tests/e2e`, run by `make e2e` — and is outside `verify` until its browser
install is dependable. The component catalog is still an open decision, and deliberately so.

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

1. **Make `make e2e` blocking, once it is dependable.** It is in report mode on purpose: the gate
   turns on last. Two things have to hold first — the Chromium download must complete reliably
   (three attempts on this workstation stalled mid-extraction and had to be cleared by hand, which
   is why `ABOM_E2E_CHANNEL=chrome` exists as an escape hatch), and the suite has to run clean
   repeatedly rather than once. Then it joins `verify`, and CI runs the same target.
2. **A populated-corpus suite.** The current specs run against a database the target creates and
   throws away, so they reach the first-run states and nothing else. Grids, wrapping under real
   labels and the Explorer under load need `make e2e ABOM_E2E_DB=<a copy>`, and the assertions that
   only make sense there are not written. A crawl of a declared organization produces the copy.
3. **Shared fixtures.** The empty database is honest but narrow, and a hand-built graph fixture
   drifts from real ingestion. A seeded corpus produced by the real pipeline, small enough to keep,
   is what both this suite and any catalog should share.
4. **Decide the component catalog.** Deferred rather than answered: Playwright covers geometry and
   integration, which was the row with no owner, and a catalog answers a different question — the
   appearance of a state no database produces. The alternatives stay named: **BlazingStory**,
   Blazor-native with no npm, whose own README calls it a personal hobby project; **a dev-only
   `/stories` route**, no new dependency and the chrome is ours to build; **neither**, with seeded
   fixture databases. Three entries in the capture matrix still need a specific database today —
   "nothing started", "no advisories", "server unreachable" — and the first two are now reachable
   from an empty one.

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
so it reports, and so the run breaks when somebody fixes it. A skip reports as a pass.

**What bUnit can state without a browser, it should.** A layout constraint expressed as a DOM count
needs no CSS engine, and the browser suite is minutes where bUnit is seconds.

## Verification

- `make e2e` against a fresh database: 38 checks, about a minute.
- For any new test: break the production code on purpose, watch it fail, restore.
- For any geometry claim, read `window.innerWidth` in the same call as the measurement — a
  requested 1920 can be reported as something else, and a media query that never matched looks
  exactly like a dead layout.
- `make e2e` refuses to run against `ABOM_DB`, so a suite that writes cannot reach the working
  corpus.
