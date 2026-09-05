# Interface testing

**Status:** layer 1 done, layers 2 and 3 pending.

## Why this exists

`make ui` runs `dotnet build`. It proves the Razor parses and asserts nothing about what renders.
Every interface regression this project has shipped was invisible to it: a filter that answered
before it asked, controls enabled with nothing to control, filters ordered so a reader had to
learn the ordering to find one, a drained queue claiming complete coverage.

## Three layers, and what each cannot see

| Layer | Owns | Blind to |
| --- | --- | --- |
| `make ui-test` (bUnit) | Behavior, markup, state transitions | Anything visual — it has no CSS engine |
| Component catalog *(pending)* | Appearance of a state, without a database | Regressions nobody looks at |
| `verify-abom-ui` screenshots | Layout, density, colour, the invariant audit | Requires a human with working vision |

Green in one says nothing about the others. The wrapping state dot and the 288px grid overflow
both passed bUnit and were only ever going to be caught by looking.

## Done

`ui/tests/Abom.WebAssembly.Tests/` — bUnit 2.9.0, xunit.v3, net10.0, in `make verify` and CI.
10 tests, each named after a regression that actually shipped.

Non-obvious things the harness encodes, all of which cost time to find:

- `JSInterop.Mode = Loose`; MudBlazor touches the DOM constantly.
- `AddMudServices(o => o.PopoverOptions.CheckForPopoverProvider = false)`. **Not**
  `RenderTree.Add<MudPopoverProvider>()` — it has no `ChildContent`, so bUnit refuses it.
- HTTP faked at `HttpMessageHandler`, so the server's JSON contract stays under test rather than
  being mocked away.
- `Withhold(path)` holds a response open. The bug worth catching lives *while a request is in
  flight*, and a stub that answers instantly cannot see that window.
- `ui/Abom.WebAssembly.csproj` needs `<Compile Remove="tests/**" />` or the SDK glob compiles the
  test project into the app.

**A test that passes against deliberately broken code is theatre.** The first version of the
flagship test passed with the fix removed, because it asserted a string that filter never shows.
Reintroduce the bug and watch the test fail, every time.

## Pending

1. **Explorer coverage.** Needs `JSInterop.SetupModule("./js/graph.js")`. Its selection state has
   two independent halves (`_isolated`/`_selected` in C#, `it.selected` in JS) and the escape
   hatches route through both, so it is a phase of its own rather than a few more tests.
2. **Component catalog spike** — see the decision section below.
3. **Shared fixtures.** `DiscoveryStateBuilder` should feed both tests and catalog stories, so a
   story cannot drift into a state the application never produces and a test cannot assert
   against one nobody ever sees.
4. **Story-driven captures.** Three entries in the capture matrix need a specific database today
   — "nothing started", "no advisories", "server unreachable". Those are exactly the states a
   catalog renders for free.

## Open decision: BlazingStory, our own, or neither

BlazingStory is a Storybook clone for Blazor, Blazor-native with no npm — which fits this
repository's constraints unusually well.

Against it, from its own README: *"a personal hobby project and a technical exhibition… it may be
abandoned if I cannot spend enough time on it"*, and *"assembly files embed the project file path
as metadata"*. Taking a hobby dependency into the verification path of a supply-chain security
tool deserves a deliberate decision rather than a default. Its MCP feature is Blazor Server only
and does not apply to this WebAssembly app.

The alternative is a dev-only `/stories` route inside the existing app: no new dependency, reuses
the theme and services, excluded from release publish — but we build the chrome ourselves.

Third option: neither, and instead seed seeded fixture databases for the awkward captures.

Spike criteria, to be settled before writing code: cost to add one story; what happens if
upstream stops; survival of a MudBlazor major bump; whether it renders a state without a
database.

## Verification

- `make ui-test`, and `make verify` end to end.
- For any new test: break the production code on purpose, watch it fail, restore.
