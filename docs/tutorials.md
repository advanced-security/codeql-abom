# ABOM Tutorials

Use the collapsible **ABOM Tutorials** section above the home page's scope cards or in Settings.
Each title describes the lesson in its tooltip and starts it when clicked. The play icon starts
a lesson; the restart icon repeats a completed one. An unfinished lesson explicitly resumes at
its previous step, including its selected example, while this tab remains open.
The first visit offers a welcome, not an automatic tour. Not now dismisses it and turns
Tutorial off; choosing a lesson explicitly enables a walkthrough.

The current task stays bright while surrounding controls are dimmed. When prompted, click the
highlighted link or control in ABOM itself; there is no separate tutorial search.
Use Back and Next between topics. **Exit tutorial** or Escape
ends it immediately. Refresh does not resume an overlay. Finishing records walkthrough
completion, not a saved request, passing query, deployment or resolved vulnerability.

| Walkthrough | What it connects |
| --- | --- |
| Find your way around ABOM | Settings, Coverage, CodeQL Requests/Catalog/Runs/Suites, then Inventory Actions and Workflows |
| Understand the QLaaS lifecycle | Authoring marks, version stages, runs and comparisons |
| Request a new detection rule | Examples, a request, fail-first tests, AST inspection, small predicates and wider validation |
| Fix false positives or false negatives | An existing custom query, contradictory examples, regression tests and a corrected version |
| Investigate an advisory or finding | Attributed evidence, references, direct/transitive callers and coverage limits |
| See what depends on an Action | Owned or third-party Actions, versions, users and dependency context |

Tutorials use the current dataset and do not require [Demo](demo.md) data; in a Demo their progress is kept per dataset. If an item is missing, the
current step says so and lets you continue without inventing a result or adding extra steps.
The progress wheel uses the fixed lesson length shown from the start. Suggested next tutorials
appear on the last step; choosing one finishes this lesson and starts the named lesson.
Request forms remain read-only previews. Editor/MCP tools mentioned in lessons are not executed
by the walkthrough.

Finish or discard an open request/run draft before starting. Work started in this tab keeps
Tutorial unavailable until its status is known to have finished, so a tour cannot obscure its
stop/cancel controls. Other tabs and background discovery remain independent.

Acknowledgement and completion are stored in this browser for the app's base address, not in
the ABOM database or a user account. Storage failures are reported; progress then lasts only
for the tab. Substantive lesson revisions may require repeating that lesson without showing
the welcome again. Unfinished steps and selected examples are held only in memory; reload starts
them afresh rather than automatically resuming an overlay.

The header keeps discovery's live state and controls on a coordinator. On a read-only instance it
shows the recorded queue with an explicit explanation that live discovery state and controls are
unavailable, rather than hiding the tracker.

## CodeQL content sources

The CodeQL lessons follow the QL MCP server's resources:

- [`codeql://learning/test-driven-development`](https://github.com/advanced-security/codeql-development-mcp-server/blob/main/server/src/resources/ql-test-driven-development.md):
  red, green, refactor; positive, negative and boundary cases.
- [`codeql://learning/query-basics`](https://github.com/advanced-security/codeql-development-mcp-server/blob/main/server/src/resources/learning-query-basics.md):
  typed query structure, metadata, compilation and testing.
- [Query unit testing](https://github.com/advanced-security/codeql-development-mcp-server/blob/main/server/src/resources/codeql-query-unit-testing.md):
  fixture sources, `.qlref`, `.expected`, `.actual` and extracted test databases.
- [Advanced TDD prompt](https://github.com/advanced-security/codeql-development-mcp-server/blob/main/server/src/prompts/ql-tdd-advanced.prompt.md):
  inspect non-empty AST output, navigate library definitions, quick-evaluate predicates, combine
  incrementally and use evaluator logs for performance investigations.
- [Actions AST reference](https://github.com/advanced-security/codeql-development-mcp-server/blob/main/server/src/resources/languages/actions_ast.md):
  workflows, jobs, steps and expressions represented in the extracted code.

ABOM's stricter [fail-first contract](architecture.md#what-produces-the-data) still applies:
fixture marks establish expectations before implementation. Accepting current query output
as expected results is not the ABOM authoring workflow. Compilation proves syntax and types;
tests and comparisons provide separate evidence about behavior.

## Reading an agent's evidence

Open **CodeQL → Audit log**, or the Audit log icon (a scroll) on a Request or Run row. These are
the same records; a query-only search is broader than a proven session/run relation. A tab opened
before the server was restarted keeps running the interface it loaded, so a tab added since is
not there yet; the page says so and offers **Reload**.

1. Each agent session is one card: how long it ran against its time limit, its AI credit cap
   (Copilot's, when set), what Copilot reported it used, and its events and refusals. The
   refusals open in the Audit log. Its details are tabs inside the card, one at a time: its
   events, the agent's last message, **Limits and permissions**, and the trees it printed.
   **Not recorded** means unknown, not current defaults. Drafting and query authoring have
   different capabilities. Check the session state too: a recorded policy can precede a startup
   failure, including a credit cap the installed runtime does not support.
2. Expand an event to see safe arguments, its policy reference, exact correlation where supplied,
   measured tests, input digests and existing printed-tree links. Totals count events, not calls.
3. Turn on **TDD and reproduction evidence** to distinguish initial measured red tests, subsequent
   iterations and stopping reasons. Compilation failures and agent messages never prove red/green.
4. Search and filters apply as you type or choose; there is nothing to submit. Each shows as a
   chip naming what it scopes to (a run by its queries, a request by its title, a session by its
   purpose), and closing a chip removes that filter alone. Share the URL. The page arrows keep a
   fixed cutoff; **Follow**, on by default, shows newer matching events as they are recorded, or
   says they are waiting if you have paged away. Refresh starts a new snapshot; clearing all
   filters returns to the global view.
5. Export matching JSONL for the full filtered cutoff, or a session's reproduction Markdown.
   Check the final completion marker. Exports are unavailable in Demo.

The guide is evidence for manual investigation, not automatic replay. Obtain the matching query,
pack dependencies, fixtures, expectations and toolchain independently, and compare their recorded
digests. Source/patch bodies and scratch are not archived; missing or changed inputs are a limit,
not permission to substitute current files and claim the original result.
