# Interface testing

**Status:** four layers, all four in `make verify`, and the browser suite blocks a pull request
through its own CI job. It resolves its own browser rather than trusting a download. `make corpus`
builds a deterministic populated database offline; the suite does **not** run against it by
default, because one missing advisory keeps four assertions red. The component catalog is still an
open decision, and deliberately so.

**Two corpora, two jobs, not interchangeable.** A crawled organization answers *did this change the
real numbers*, and only ever exists where somebody has a token and has waited. The fixture corpus
answers *does this still work where nobody has crawled anything*, needs no CLI, no network and no
token, and is the only one that can gate a pull request. Neither substitutes for the other, and a
claim measured against one must say which.

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

1. **Seed an advisory into the fixture corpus, because nothing else will move the gate.**
   `make corpus` builds one offline from recorded CodeQL output — **7 repositories, 70 nodes, 37
   edges, 18 findings**, all four edge kinds, both reference styles, a dependency between two
   distinct owned repositories, a local composite action, two container images, and **0
   advisories**. Growing it from 3 repositories and 20 nodes to that changed the browser suite by
   **nothing**: it ran **48 passed, 5 failed, 7 skipped** before and after, which is the measurement
   that matters here, because it says node count was never the blocker.

   Four of the five failures are one missing fact — there is no advisory, so the advisories unit is
   empty, its columns are all zero, it has nothing to overflow at 1280px, and the coverage strip
   renders a count the grid does not divide. **An advisory is not CodeQL output**, so
   `scripts/regenerate-corpus-fixtures.sh` cannot produce one however many repositories are added
   to it: it comes from GHSA, and the fixture needs a recorded advisory payload and a package that
   matches something the corpus already uses.

   The fifth — *switching force preserves angle* — is not a fixture problem at all and survived the
   growth untouched. It is the force layout having no fixed seed, which is
   [`graph-layout.md`](graph-layout.md)'s subject, and it will not go green here whatever the
   corpus holds.

   **Until that run is green the e2e default stays the empty database**, because a gate that is red
   for everyone teaches nobody anything.
2. **Make the populated run a gate once it is green.** This is the point of the corpus and it is
   not reached by item 1 alone: the run has to be wired into the CI job that blocks a pull request,
   alongside the empty-database run rather than instead of it. The two answer different questions
   and a gate that holds only the empty one is what let a whole class of defect through — every
   product bug this corpus has caught was invisible to the empty database, and so was an
   accessibility regression that reached `/settings` and nothing else. Depends on 1.
3. **More populated-corpus assertions.** The suite branches on `corpusSize` and two checks now use
   it — the coverage strip, which orphans a cell only when nothing is declared, and the graph's
   share of the window, which needs a graph to measure. Grids under real labels, wrapping under long
   repository names and the Explorer under load are still unwritten.
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
