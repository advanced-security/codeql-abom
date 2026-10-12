# Learn ABOM through repeatable, read-only tutorials

**Status:** planned for first implementation; no Tutorial code exists.

## Why this exists

ABOM's page-level controls do not explain how a request becomes a tested query, how a run supports
a version's next stage, or how evidence becomes an investigation of dependencies and reach.
The first user-facing release needs those connections without making a user modify their data
just to learn the interface.

## Scope and decisions

Build six repeatable walkthroughs: orientation, QLaaS lifecycle, new rule, FP/FN improvement,
blast radius, and Action dependents. They use current pages and any current dataset, not a
dedicated demo dataset.

A normal-data tour is read-only. It can navigate, reveal rows, select a local scope and display
an unsaved read-only form preview; it never submits requests, invokes authoring, runs/tests
queries, changes stages or suites, fetches examples, starts discovery or writes to GitHub.
Users perform real work after exiting the walkthrough.

[Demo mode](../tools/demo-mode.md) is a later dependent workstream, never a prerequisite.
Tutorial definitions carry stable semantic step IDs and outcomes so Demo can add an explicitly
labelled Practice adapter without replacing the lessons or spotlight engine.

No external tour framework, server-side progress store, reader login implementation, editor,
analytics, pack publication or graph layout redesign is included.

## User experience and persistence

1. Put a labelled **Tutorial** checkbox in the shared app chrome, accessible on every route.
   An optional duplicate beside the home checklist binds to the same service state.
2. Initialize preferences before showing a welcome. First visit defaults to enabled and shows
   a brief welcome, acknowledgement and tutorial choices, never an automatically launched tour.
3. Acknowledge records acknowledgement and turns Tutorial off. Starting a tour also acknowledges
   the welcome, explicitly enables Tutorial and dismisses the welcome.
4. Unchecking Tutorial acknowledges the welcome and immediately ends the current tour. It does not
   return on reload or another page. Checking it opens choices, not an automatically resumed tour.
5. Start/Repeat explicitly enables a tour. Finish/Exit returns to the non-touring state with
   Tutorial off until another deliberate start.
6. Back, Next, **Exit tutorial**, Escape and the checkbox remain usable while other UI is obscured.
   Exit never marks completion. Finishing the last step does.
7. Store a small versioned localStorage record, namespaced by absolute app base URI and feature
   schema. Add a separate demo dataset ID to the namespace when Demo exists.
8. Persist acknowledgement, enablement and completion by stable tutorial ID/content version,
   not source code, node IDs, filters, query bodies or sensitive dataset content.
9. Active step/target are transient. Refresh clears the overlay and permits explicit restart;
   it neither nags an acknowledged user nor implies a partially viewed tour is complete.
10. A substantive lesson revision invalidates only that lesson's completion, not acknowledgement.
11. Storage failure is visible: progress works for the tab with a notice that it will not be
    remembered. Malformed data is reported and resets only tutorial preference data.

Acknowledgement, enablement, completion and active step are distinct. Merely enabling Tutorial
or displaying its welcome is not an active walkthrough and does not disable normal operations.

### Home checklist

Keep the existing scoped Inventory entry cards, graph link and coverage entry; the home page
still provides a useful way into the estate. Add a compact learning section containing all six
lessons, visible even with Tutorial off. Each has a completion indicator and Start/Repeat action.
Completed means **walkthrough finished**, never **workflow performed**.

Use a green check plus the word Completed, not color alone. The completion indicator is not a
manually editable checkbox; the Tutorial on/off checkbox has a different label and purpose.
Keep the checklist small, usable at narrow widths, and separate from existing operational counts.

## Six lesson definitions

Every step has a brief title, at most two short explanatory sentences, one focus target and
optional expandable detail. Explain the reason and next connection, not every control on a page.

### 1. Find your way around ABOM

Home scope entry -> Inventory units/filters -> node detail/figure -> Explorer scope chooser ->
CodeQL tabs -> Settings/coverage. Explain declared ownership versus seed/upstream provenance,
direct versus transitive, and what is unknown. Do not change ownership or start discovery.
Finish by distinguishing walkthroughs from real operations.

### 2. Understand the QLaaS lifecycle

Requests -> session/evidence -> catalog version -> tests -> scoped run -> comparison -> suites.
Cover every authoring icon from the shared mapping in
[`AuthoringSteps.razor`](../../ui/Shared/AuthoringSteps.razor):

| Mark | Terse explanation |
| --- | --- |
| Target | The chosen new or existing query ID; a decision, not a passing test |
| Query help | What is detected, why it matters and how to remediate |
| Expected results | Expectations specified from examples before the query; never accepting output as truth |
| Test database | Extracted fixture code, finalized and current with its sources |
| AST | The syntax tree printed from those fixtures to find the relevant CodeQL structure |
| Compiles | This version compiles against the resolved libraries/toolchain |
| Tests | Non-compliant and compliant cases pass, with the required fail-first evidence |

Explain passing, failed, stale/unconfirmed and not-yet marks with words and shape as well as
color. Reuse/extract the current label/icon/state mapping rather than copying icon constants into
a tutorial-only catalog that drifts.

Separate authoring marks from query version stages:

| Stage | Meaning and prerequisite |
| --- | --- |
| `dev` | A development version; saving source does not schedule it across the estate |
| `test` | This exact version's own tests pass, not merely the pack's exit status |
| `extended-test` | Required authoring evidence passes; target belongs to the request, not this gate |
| `review` | A finished, current-CodeQL comparison for the exact version covers at least one repository |
| `deploy` | Explicit local suite intent, not publishing a pack or creating GitHub alerts |

Also distinguish request status, session working/finished/stopped/failed/interrupted states, run
status, query content-hash identity and toolchain staleness. Stage refusals explain prerequisites;
they are not obstacles the tutorial can bypass.

### 3. Ask for a new detection rule

CodeQL Requests -> New request/read-only form preview -> identity and target -> metadata,
recommendation and references -> compliant/non-compliant examples -> eventual request/session ->
catalog/tests/run/comparison -> deliberate local deployment.

Explain choosing a valid new custom ID and operator-pack boundaries. Examples become tests,
including a failing stub before implementation. Explain Save, Start session and Read linked
example without invoking them. Never claim a query was created, tested or deployed without evidence.

### 4. Improve a catalog rule's false positives or false negatives

Choose an operator-pack catalog query -> exact ID/version/source -> relevant run/results ->
improvement request preview with existing ID -> wrong/missed/both and examples -> fail-first
regression -> new version -> rerun the same scope -> compare and re-earn stages.

Metadata of an existing catalog query stays read-only. An upstream query outside the operator's
pack is not an editable target. Cover both FP and FN examples, not just generic "improvement".
Do not teach the obsolete accept-the-output flow.

### 5. Investigate the blast radius of evidence

Inventory Advisories or Findings -> explicitly selected advisory/finding/rule and node ->
references and evidence -> who reaches it and by which routes -> owned/source split ->
Explorer answering the same scoped question -> coverage limits.

One checklist item has explicit Advisory and Finding branches; either can complete the learning
walkthrough, and both must be tested. An advisory is an attributed publication, not proof that
an observed reference is vulnerable. A finding names a rule and location, not a verdict that
every dependent repository executes or is vulnerable to that finding. Direct/transitive reach,
attribution and unanalyzed paths remain visible. Counts and filters retain their actual scope.

### 6. See what depends on an Action

Inventory dependencies -> choose owned or third-party Action -> node Versions in use ->
selected ref/commit and governing lock -> Who uses it -> direct/transitive route detail ->
owned versus upstream inclusion -> focused figure/Explorer.

Separate **what depends on this Action** from **what this Action depends on**. Cover internal
owned and third-party Actions without claiming origin or the presence of a database establishes
ownership.

## Dataset independence and empty states

Choose targets deliberately from current data; never hard-code corpus keys or IDs. Resolve one
stable target per step, not whichever row a refresh puts first. Offer a small read-only picker
for a query, evidence item or Action where a user selection is needed.

If there is no matching data, no operator pack, no authenticated operator, a failed API read or
an unfinished load, say which condition applies. Empty-data steps may teach through clearly
labelled explanatory panels and the page's actual empty state. Never insert fake catalog rows,
invent counts or imply that analysis ran.

A missing required target pauses progression and offers Retry, an explicitly labelled
explanation, or Exit. The explanatory branch can finish the learning walkthrough only after the
user views and finishes it; it does not complete the underlying operational task.

Render form previews from a shared read-only variant of
[`QueryRequestForm.razor`](../../ui/Shared/QueryRequestForm.razor), with no Save, linked fetch,
agent-draft invocation or initialization side effect. Preserve real unsaved requests and run
drafts; tutorial examples never replace them.

## Spotlight architecture and accessibility

- One `TutorialHost` in [`MainLayout.razor`](../../ui/Layout/MainLayout.razor) renders a neutral
  translucent scrim, cutout and coach panel in a known top-level overlay root. The real target
  retains its normal brightness, theme and colors. Do not clone/reparent controls.
- Use stable semantic `data-abom-tutorial-target` values; reuse existing unambiguous `data-abom-*`
  anchors where useful. Resolve repeated rows using the selected query/node identity locally.
- A small JS module measures DOM target geometry. Highlight controls, rows, panels, figures or
  the whole graph canvas, not individual WebGL nodes. Do not recolor or relayout the graph.
- Block outside input for pointer and keyboard, not merely through fading. Do not make an
  ancestor inert when it contains the target. Focus stays within permitted read-only target
  controls and coach controls; restore prior focus on exit.
- Highlighted mutation controls are still semantically disabled/guarded in a normal walkthrough.
  The scrim alone is not a read-only guarantee.
- Reposition on scroll, resize, route changes, target ResizeObserver updates and MudBlazor
  popover/dialog transitions. A vanished target clears the stale cutout and shows recovery;
  it never quietly advances. Tear down every observer/listener on exit.
- Scroll into view only on explicit step changes. Wait for known page/render readiness, not
  arbitrary timers. For oversized targets, focus the visible panel region and keep the coach
  from obscuring the content being explained.
- Support keyboard Back/Next/Exit, Escape, screen-reader headings/step announcements, focus
  restore, reduced motion, dark/light themes, narrow screens and zoom.
- Keep the Tutorial checkbox reachable without leaving unrelated chrome interactive under
  the scrim. Preserve its legible label when the app bar is crowded.
- Use [`ScopeService`](../../ui/Services/ScopeService.cs) and
  [`ViewSpec`](../../ui/Services/ViewSpec.cs) for filter/address semantics. Tour state is not a
  predicate term and is never sent to predicate APIs.
- Snapshot local view choices at start and restore them on controlled exit without overwriting
  independent changes or popping browser history. Unexpected navigation safely exits; guided
  navigation waits for its target page. Back/reload cannot strand an overlay.

## Read-only action policy

Define `inactive`, `walkthrough` and future `demo-practice` contexts. During a walkthrough,
suppress automatic behavior that starts control requests, notably Settings identity refresh;
read-only data refresh remains available.

Add an outbound request guard at the shared HttpClient handler boundary, before dispatch, so
every non-read API operation is refused while a read-only tour is active. This must include
direct `http.SendAsync` paths in [`AbomApi.cs`](../../ui/Services/AbomApi.cs), not just the common
control helper. Surface a clear tutorial refusal through existing notification/error conventions,
not a fabricated successful response. Component event guards normally prevent the backstop firing.

This is a browser-session learning policy, not server authorization. No server-wide read-only
switch is introduced; other tabs and independent discovery are not altered by a tutorial.
Finish/discard a pre-existing unsaved edit through existing controls before starting a tour.
Do not silently save/discard it. Disable tour start while that same tab has a submitted cancellable
operation whose controls would be obscured.

## Pending

1. **State and policy, test-first.** Add definitions, preference record, Tutorial service and
   action policy. Register the services and request guard in
   [`Program.cs`](../../ui/Program.cs) and mirror them in
   [`AbomTestContext`](../../ui/tests/Abom.WebAssembly.Tests/AbomTestContext.cs).
   Test acknowledgement, checkbox, Start/Repeat, incomplete Exit, refresh and storage-error
   transitions, plus every outbound write path. Depends on nothing.
2. **Spotlight host, test-first.** Add shared host, CSS and focused JS interop module. Test
   geometry/lifecycle and accessible control/focus state. Depends on 1.
3. **Six lessons and checklist, test-first.** Wire Home/layout, CodeQL tabs, read-only request
   preview, authoring marks, catalog, runs/results/comparison, Inventory, node/figure and Explorer
   targets. Preserve existing home scope entries and share existing icon mappings.
   Depends on 1 and 2.
4. **Browser qualification and documentation.** Add tutorial-specific Playwright specs and
   explicit acknowledged setup for existing non-tutorial geometry tests. Keep separate fresh
   first-visit tests. Update [vocabulary](../../docs/vocabulary.md),
   [architecture](../../docs/architecture.md) and terse user help only for behavior actually built.
   Verify fixture and real-data copies in both themes with screenshots. Depends on 3.

## The constraints that must not be broken

Tutorial completion is a learning marker, never readiness, vulnerability, ownership or deployment
evidence. Findings stay rule-attributed; advisories enrich rather than adjudicate. No step earns
a stage, accepts generated output as expected results or changes a live scope silently.
Demo adapts to Tutorial; Tutorial never requires Demo.

## Verification

- Write tests first and observe failure for the intended behavior before implementation.
- bUnit/service tests cover the full transition table, read-only previews, selected-row stability,
  incomplete Exit, Repeat and unchanged controls when Tutorial is inactive.
- JS tests cover cutout geometry, vanished targets, resize/scroll, focus and teardown.
- Playwright covers all six tours, both evidence branches, empty/partial data, deep links,
  disabled mutations, keyboard-only use, back/reload, both themes, 1440px and 920px widths,
  narrow screens, zoom and reduced motion.
- Assert the target's computed appearance matches its non-tour baseline while an outside
  element is under the scrim. Review screenshots for the actual visual result.
- Capture all walkthrough requests: no control API request is dispatched, including automatic
  initialization and keyboard activation. Local tutorial preference writes are permitted.
- On a real-corpus SQLite `.backup`, capture affected endpoints and application-table metrics
  before/after all tours. Unchanged data and byte-identical endpoint responses demonstrate
  preservation; distinguish existing read-side bookkeeping from business mutations.
- `make verify` is the definition of verified. Check `make status` and pause discovery before
  CodeQL-backed validation. Follow the `verify-abom-ui` screenshot procedure. No CodeQL command
  is added to a Tutorial step.

## Coordination

[Query requests](codeql-requests.md) retains its tag/catalog-width refinements and corpus
acceptance correction. Tutorial does not depend on those finishing, but must not teach the
obsolete flow. [Demo](../tools/demo-mode.md) coordinates the narrow correction needed for training.
[Graph layout](graph-layout.md) retains its renderer/layout work; tours use DOM and figures
without treating a broken canvas as healthy or making its redesign a prerequisite.
