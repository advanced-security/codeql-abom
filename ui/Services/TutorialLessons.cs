namespace Abom.WebAssembly.Services;

public sealed record TutorialStep(string Id, string Title, string Text, string Route, string Target,
    string Detail = "", string Preview = "", string Action = "", string Empty = "");

public sealed record TutorialLesson(string Id, string Title, string Description,
    IReadOnlyList<TutorialStep> Steps, int Version = 2)
{
    public IReadOnlyList<string> Related => Id switch
    {
        "orientation" => ["lifecycle", "new-rule"],
        "lifecycle" => ["new-rule", "improve-rule"],
        "new-rule" => ["improve-rule", "blast-radius"],
        "improve-rule" => ["lifecycle", "blast-radius"],
        "blast-radius" => ["action-dependents", "improve-rule"],
        _ => ["blast-radius", "lifecycle"],
    };
}

public static class TutorialLessons
{
    public const string ActionLink = "[data-abom-tutorial-action] a[href]";
    public const string QueryLink = "tr:has([data-abom-query-working='true']) [data-abom-query-detail]";
    public const string ReferenceLink = "[data-abom-version-ref]";

    public static readonly IReadOnlyList<TutorialLesson> All =
    [
        new("orientation", "Find your way around ABOM",
        "Start with Settings and coverage, then learn CodeQL and the Inventory.",
        [
            new("settings", "Choose what ABOM manages", "Settings is where you choose your accounts and repositories, and configure discovery.", "/settings", "[data-abom-identity]"),
            new("coverage", "Check what has been analyzed", "Coverage shows which dependencies are analyzed, waiting or unavailable. Check it before drawing conclusions from a result.", "/inventory?unit=coverage", "[data-abom-unit-description]"),
            new("requests", "Request a detection rule", "Requests describe what a query should detect, using examples it should report and examples it should leave alone.", "/codeql?tab=requests", "[data-abom-requests]"),
            new("catalog", "Follow each query version", "The Query catalog shows the rules ABOM knows, their test evidence and their stage.", "/codeql?tab=catalog", "[data-abom-queries-table], [data-abom-queries-empty]"),
            new("runs", "See where queries ran", "Runs show the selected repositories, what was analyzed and what the queries found.", "/codeql?tab=runs", "[data-abom-runs]"),
            new("suites", "Choose what discovery runs", "Suites control which queries discovery uses. Changing a local suite does not publish a pack or create GitHub alerts.", "/codeql?tab=suites", "[data-abom-suites]"),
            new("inventory", "Inspect your inventory", "Inventory brings Actions, findings, advisories and workflows together. Use its tabs and filters to narrow the list.", "/inventory", "[data-abom-inventory-units]"),
            new("action", "Open the first Action", "Click the highlighted Action name in the Actions list to open its details.", "/inventory?unit=dependencies", ActionLink, Action: "node",
                Empty: "No Actions are listed yet. Discovery populates this list; continue to see the rest of the workflow."),
            new("detail", "Read the Action's details", "The details page shows its versions, what uses it and any recorded findings or advisories.", "$node", ".abom-node-head",
                Empty: "No Action was opened, so there are no details to show in this walkthrough."),
            new("workflows", "Find the workflows to investigate", "Workflows connects the results to the files your repositories run. Continue with a CodeQL tutorial to learn the detection workflow in detail.", "/inventory?unit=workflows", "[data-abom-unit-description]"),
        ]),
        new("lifecycle", "Understand the QLaaS lifecycle",
        "Follow a request through authoring, tests, a run and local deployment.",
        [
            new("requests", "Begin with a request", "A request describes what to detect and supplies examples. Its status is separate from the query version's stage.", "/codeql?tab=requests", "[data-abom-requests]"),
            new("session", "Follow the authoring session", "A session records the author's work and tests. A finished session does not automatically advance the query.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Preview: "sessions"),
            .. AuthoringMarks(),
            new("catalog", "Check the exact version", "Changing a query or an imported library creates a new version. Tests and run results must match that version.", "/codeql?tab=catalog", "[data-abom-queries-table], [data-abom-queries-empty]"),
            .. Stages(),
            new("run", "Run on a chosen scope", "A run evaluates selected queries on selected repositories. Check both the results and how much of the scope was analyzed.", "/codeql?tab=runs", "[data-abom-runs]"),
            new("comparison", "Compare with code scanning", "Compare a completed regular run with the alerts developers see. Review requires a matching version and CodeQL, with at least one repository compared.", "/codeql?tab=runs", "[data-abom-tutorial-preview]", Preview: "comparison"),
            new("suites", "Adopt a query deliberately", "Deploy puts a version in a local suite that discovery can use. It does not publish a pack or upload alerts.", "/codeql?tab=suites", "[data-abom-suites]"),
        ]),
        new("new-rule", "Request a new detection rule",
        "Turn examples into a request, fail-first tests and a validated custom query.",
        [
            new("request", "Begin with a new request", "New request opens the form. This walkthrough uses a read-only preview so nothing is submitted.", "/codeql?tab=requests", "[data-abom-request-new]"),
            new("identity", "Name the request and query", "Give the request a clear title. A new custom query ID requests a new rule; leaving it empty lets the author choose.", "/codeql?tab=requests", "[data-abom-tutorial-preview] [data-abom-request-query]", Preview: "form"),
            new("examples", "Show both sides of the rule", "Compliant examples must be left alone. Non-compliant examples must be reported.", "/codeql?tab=requests", "[data-abom-tutorial-preview] .abom-request-examples", Preview: "form"),
            new("test-contract", "Define the expected result", "Start with a small positive case, a negative case and an edge case. Their expectations are the test contract.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Detail: "A .qlref names the query. CodeQL extracts the fixture into a .testproj database and compares .actual with .expected; ABOM requires fixture marks before implementation.", Preview: "test-contract"),
            new("guidance", "Explain the fix", "Describe what developers should do instead and include useful references. This becomes the query's help.", "/codeql?tab=requests", "[data-abom-tutorial-preview] [data-abom-request-recommendation]", Preview: "form"),
            new("metadata", "Describe the finding", "Severity, precision and tags help readers interpret and filter results. They do not replace testing.", "/codeql?tab=requests", "[data-abom-tutorial-preview] [data-abom-request-metadata]", Preview: "form"),
            new("author", "Save and start authoring", "Outside this walkthrough, Save records the request and Start session works on it in your query pack.", "/codeql?tab=requests", "[data-abom-requests]"),
            new("tests", "Prove the test fails first", "The empty query must miss the marked cases before implementation. Then a corrected query can demonstrate the intended change.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Preview: "tests"),
            new("inspect-ast", "Inspect the fixture's AST", "Find the workflow, job or step in the extracted syntax tree before choosing CodeQL classes and predicates.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Detail: "QL MCP's PrintAST should return nodes with source locations, not just headers. Use the Actions AST reference and library definitions instead of guessing member names.", Preview: "ast"),
            new("query-shape", "Build one piece at a time", "Use from for typed values, where for constraints, and select for the reported location and message.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Detail: "Compile to catch syntax and type errors. Tests check whether the query reports the right code.", Preview: "query-shape"),
            new("quick-evaluate", "Check individual predicates", "Quick-evaluate a predicate on the fixture before combining it with the others. Inspect unexpected matches and missing cases.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Detail: "Use QL MCP's quick_evaluate in the editor. If results are empty, check extraction and the AST before relaxing constraints.", Preview: "quick-evaluate"),
            new("catalog", "Inspect the resulting version", "The Query catalog connects the authored version to its help, tests and recorded runs.", "/codeql?tab=catalog", "[data-abom-queries-table], [data-abom-queries-empty]"),
            new("refactor", "Keep the tests green", "Add realistic variations and refactor without changing the expected detections. Re-run related tests after changing a shared library.", "/codeql?tab=catalog", "[data-abom-tutorial-preview]", Detail: "Check unexpected, missing and duplicate results. Use evaluator logs to investigate a slow query.", Preview: "refactor"),
            new("run", "Validate on a wider scope", "Run the version on selected repositories, inspect coverage and results, then compare with code scanning.", "/codeql?tab=runs", "[data-abom-runs]"),
            new("deploy", "Choose when discovery adopts it", "A deployed version joins a local suite. Bind that suite here when you want discovery to use it.", "/codeql?tab=suites", "[data-abom-suites]"),
        ]),
        new("improve-rule", "Fix false positives or false negatives",
        "Use contradictory examples and regression tests to improve your query.",
        [
            new("choose", "Open a query from your pack", "Click the highlighted row control to inspect a custom query's version and evidence.", "/codeql?tab=catalog", QueryLink, Action: "query",
                Empty: "No working query from your pack is listed. You can still follow how an improvement request is prepared."),
            new("version", "Inspect its current behavior", "Read its version, metadata and previous runs. A correction creates a new version with its own test evidence.", "$query", "[data-abom-query-metadata], [data-abom-queries-table]"),
            new("understand", "Read the query and its tests", "Locate the query, its imported libraries and .qlref in your editor. Compare the predicates with the fixture AST.", "$query", "[data-abom-tutorial-preview]", Detail: "QL MCP's query-file discovery and explain-query workflows help locate the relevant source and tests.", Preview: "understand"),
            new("request", "Request an improvement", "Keep the existing query ID and describe a false positive, a missed case or both. Existing query metadata stays read-only.", "/codeql?tab=requests", "[data-abom-tutorial-preview] [data-abom-request-query]", Preview: "improve-form"),
            new("regression", "Add the case that proves the problem", "Add compliant code that was reported wrongly, or non-compliant code that was missed. Keep the earlier examples too.", "/codeql?tab=requests", "[data-abom-tutorial-preview] .abom-request-examples", Preview: "improve-form"),
            new("isolate", "Find the incorrect predicate", "Confirm the old query fails the regression, then quick-evaluate the suspect predicate against the fixture.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Detail: "Check extraction and the AST first. Use control-flow or call tools where supported; a dependency graph does not prove program execution.", Preview: "isolate"),
            new("tests", "Check the correction", "The fixed version must handle the new case without breaking earlier detections. Do not accept current output just to make a test pass.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Preview: "tests"),
            new("boundaries", "Test nearby cases", "Add realistic variations on both sides of the rule. Re-run the full related tests after the fix and after refactoring.", "/codeql?tab=requests", "[data-abom-tutorial-preview]", Preview: "boundaries"),
            new("compare", "Re-run the same scope", "Compare the corrected version with earlier results and code scanning. The new version must earn its stages again.", "/codeql?tab=runs", "[data-abom-runs]"),
        ]),
        new("blast-radius", "Investigate an advisory or finding",
        "Follow recorded evidence to the Action and repositories it can reach.",
        [
            new("evidence", "Choose Findings or Advisories", "Use the highlighted Inventory tabs to choose the kind of evidence you want to investigate.", "/inventory?unit=advisories", "[data-abom-inventory-units]", Action: "evidence-tab"),
            new("open", "Open the recorded evidence", "Click the highlighted result or related Action in the list.", "$evidence", "$evidence-link", Action: "evidence",
                Empty: "No matching evidence is listed. Coverage can show what has and has not been analyzed."),
            new("attribution", "Read what was reported", "Keep the rule or advisory beside its source and observed version. A reported issue does not mean every caller is vulnerable.", "$evidence-detail", "$evidence-detail-target", Action: "finding-asset",
                Empty: "No linked Action or workflow is recorded for this result."),
            new("versions", "Choose a reference", "Click a reference in Versions in use to narrow the details to that version.", "$node", ReferenceLink, Action: "reference",
                Empty: "No reference is recorded for this item."),
            new("users", "Read who uses it", "Direct users name it themselves. Transitive users reach it through another component.", "$node", "[data-abom-node-uses]"),
            new("limits", "Keep the limits in view", "These are recorded dependency routes, not proof that a vulnerable path runs. Unanalyzed dependencies may leave the answer incomplete.", "$node", "[data-abom-node-uses]"),
        ]),
        new("action-dependents", "See what depends on an Action",
        "Inspect an Action's versions, direct users and indirect users.",
        [
            new("choose", "Open an Action", "Click the highlighted name in the Actions list.", "/inventory?unit=dependencies", ActionLink, Action: "node",
                Empty: "No Actions are listed yet. Discovery populates this list."),
            new("identity", "Confirm what you opened", "The heading identifies the Action. Its details separate what uses it from what it uses.", "$node", ".abom-node-head"),
            new("versions", "Choose a reference", "Click a reference to see the code selected by its callers, including any governing lock.", "$node", ReferenceLink, Action: "reference",
                Empty: "No reference is recorded for this Action."),
            new("users", "Read the users and routes", "Direct users name this Action. Transitive users arrive through another Action or workflow.", "$node", "[data-abom-node-uses]"),
            new("figure", "Put the relationships in context", "The figure shows both directions. Check its scope and any missing analysis before treating the picture as complete.", "$node", "[data-abom-node-layout]"),
        ]),
    ];

    private static IEnumerable<TutorialStep> AuthoringMarks()
    {
        foreach (var (id, title, text) in new[]
        {
            ("target", "Target", "The target identifies the new or existing query. Choosing it is a decision, not a passing test."),
            ("help", "Query help", "Help explains the detection and how to fix it. It lives beside the query."),
            ("expected", "Expected results", "Examples establish what should be reported before implementation. Current query output is not the source of truth."),
            ("database", "Test database", "CodeQL extracts the fixture sources into a test database. It must match the current sources."),
            ("ast", "AST", "The abstract syntax tree shows how CodeQL represents the fixture. Use it to find the relevant classes and predicates."),
            ("compiles", "Compiles", "Compilation checks syntax and types. It does not prove the detection is correct."),
            ("tests", "Tests", "Tests compare detections with the fixture's expectations. Passing must follow an earlier failure on those cases."),
        })
            yield return new("mark-" + id, title, text, "/codeql?tab=requests", "[data-abom-tutorial-preview]", Preview: id);
    }

    private static IEnumerable<TutorialStep> Stages()
    {
        foreach (var (id, text) in new[]
        {
            ("dev", "Development records a version without scheduling it across your repositories."),
            ("test", "This exact version's own tests must pass. An empty test directory is not a pass."),
            ("extended-test", "The required authoring checks must pass. The target decision belongs to the request."),
            ("review", "A completed comparison must cover at least one repository for this version and the current CodeQL."),
            ("deploy", "A person chooses this version for the local deployed suite. No pack is published and no alert is uploaded."),
        })
            yield return new("stage-" + id, id, text, "/codeql?tab=catalog",
                "[data-abom-queries-table], [data-abom-queries-empty]");
    }
}
