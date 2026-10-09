using AngleSharp.Dom;
using Abom.WebAssembly.Models;
using Abom.WebAssembly.Pages;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Everything a person can act on in the CodeQL page says what it is for when pointed at, in a
/// styled tip. A native title shows late, unstyled, and in an embedded browser not at all, and one
/// that restates its control's label tells a reader nothing the label did not.
/// </summary>
public class CodeQLTooltipTests : AbomTestContext
{
    // What a person acts on. A text field is not here: its label is what it asks for.
    private const string Controls =
        "button, a[href], [role=tab], .mud-select, .mud-switch, .mud-checkbox, .mud-chip.mud-clickable";

    // MudBlazor's own: the pager, a header's sort arrow beside the HeaderTip that explains the
    // column, and an alert's close button.
    private const string Framework = ".mud-table-pagination, thead, .mud-alert-close";

    private const string SignedIn = """{"authenticated":true,"login":"operator","accounts":[]}""";

    private const string Requests = """
        {"requests":[{"rowId":8,"description":"stop flagging runners we own",
          "compliant":"runs-on: [self-hosted, acme]","nonCompliant":"runs-on: self-hosted",
          "queryId":"acme/self-hosted-runner","targetSource":"operator","kind":"false-alarm","status":"open",
          "requestedBy":"operator","createdAt":"2026-09-23T10:00:00Z","updatedAt":"2026-09-23T10:00:00Z",
          "sourceRunId":7,"evidence":[],
          "steps":[
            {"step":"target","state":"passing","detail":"acme/self-hosted-runner, selected by you"},
            {"step":"tests","state":"pending","detail":"never run for this version"}]}],
         "kinds":["","missed","false-alarm","both"],"statuses":["open","in-progress","done","declined"]}
        """;

    private const string Sessions = """
        {"sessions":[{"rowId":1,"requestId":8,"state":"finished","startedBy":"operator",
          "model":"claude-opus-5","startedAt":"2026-09-24T02:20:25Z","endedAt":"2026-09-24T02:22:31Z",
          "detail":"Stopped once the tests passed.","events":84,"refused":4}],
         "inFlight":[]}
        """;

    private const string Trees = """
        {"asts":[{"rowId":3,"sessionId":1,"requestId":8,"queryId":"acme/self-hosted-runner",
          "printedAt":"2026-09-24T02:21:23Z","query":"PrintAST",
          "database":"test/SelfHostedRunner/SelfHostedRunner.testproj",
          "sourceFiles":".github/workflows/ci.yml","testDigest":"abc","size":42,"truncated":false}]}
        """;

    private const string Catalog = """
        {"queries":[
          {"queryId":"acme/self-hosted-runner","queryVersion":"9b5327ab01d7","pack":"abom/custom-queries",
           "packVersion":"2.27.0","cliVersion":"2.27.0","source":"custom","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"lane":false,"stage":"","stagedBy":"","stagedAt":"",
           "tested":false,"passing":false,"testDetail":"","working":true,"requests":[8],
           "metadata":{"name":"Self-hosted runner","problem.severity":"warning"},
           "steps":[
             {"step":"help","state":"pending","detail":"no src/SelfHostedRunner.md beside the query"},
             {"step":"tests","state":"pending","detail":"never run for this version"}],
           "runs":57,"repositories":57,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T17:40:00Z"},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","pack":"codeql/actions-queries",
           "packVersion":"0.6.35","cliVersion":"2.27.0","source":"upstream","suite":"","kind":"problem",
           "kindRecorded":true,"discovery":false,"lane":false,"stage":"review","stagedBy":"operator",
           "stagedAt":"2026-09-22T15:00:00Z","steps":[],
           "runs":204,"repositories":204,"analyses":0,"firstRunAt":"","lastRunAt":"2026-09-22T10:15:00Z"}
        ],"stages":["dev","test","review"]}
        """;

    private const string Run = """
        {"rowId":7,"predicate":"repositories=octo%2Fapp,octo%2Fquiet,octo%2Fnone",
         "mode":"regular","requestedBy":"operator","state":"complete","held":true,
         "createdAt":"2026-09-22T16:00:00Z","finishedAt":"2026-09-22T16:04:00Z","results":3,
         "queries":[{"queryId":"acme/self-hosted-runner","queryVersion":"9b5327ab01d7"}],
         "tally":{"succeeded":2,"no_database":1},
         "members":[
           {"repository":"octo/app","status":"succeeded","counted":true,"results":3,
            "byQuery":{"acme/self-hosted-runner":3}},
           {"repository":"octo/quiet","status":"succeeded","counted":true,"results":0,
            "byQuery":{"acme/self-hosted-runner":0}},
           {"repository":"octo/none","status":"no_database","detail":"ABOM tracks no repository named octo/none",
            "counted":false,"results":0,"byQuery":{}}]}
        """;

    private const string Suites = """
        {"available":["ql/actions/abom/src/codeql-suites/abom-dependency-graph.qls",
                      "ql/actions/abom/src/codeql-suites/abom-findings.qls"],
         "tiers":{"direct":["ql/actions/abom/src/codeql-suites/abom-dependency-graph.qls"],
                  "transitive":[]},
         "counts":{"direct":{"total":522,"analyzed":522},"transitive":{"total":691,"analyzed":691}}}
        """;

    public CodeQLTooltipTests()
    {
        Answer("api/accounts", SignedIn);
        Answer("api/query-requests/8/sessions", Sessions);
        Answer("api/query-requests/8/asts", Trees);
        Answer("api/query-requests", Requests);
        Answer("api/queries/asts", Trees);
        Answer("api/queries", Catalog);
        Answer("api/runs/7/comparisons", """{"comparisons":[]}""");
        Answer("api/runs/7", Run);
        Answer("api/runs", $$"""{"runs":[{{Run}}]}""");
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/discovery/suites", Suites);
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().WithTiers(522, 691).ToJson());
        Answer("api/frontier", "[]");
    }

    private IRenderedComponent<CodeQL> Arrive(string tab, string ready)
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo($"/codeql?tab={tab}&request=8&run=7&query=acme%2Fself-hosted-runner");
        var page = Render<CodeQL>();
        page.WaitForAssertion(() => page.Find(ready));
        return page;
    }

    [Fact]
    public void EveryControlOnTheRequestsTabAndItsFormSaysWhatItIsFor()
    {
        var page = Arrive("requests", "[data-abom-session-said]");
        page.Find("[data-abom-request-new]").Click();
        page.WaitForAssertion(() => page.Find("[data-abom-request-form]"));

        AssertTipped(page);
    }

    [Fact]
    public void EveryControlOnTheCatalogTabSaysWhatItIsFor()
    {
        Services.GetRequiredService<RunDraft>().Add(
            new AnalysisQueryRef { QueryId = "acme/self-hosted-runner", QueryVersion = "9b5327ab01d7" });

        var page = Arrive("catalog", "[data-abom-query-requests]");
        page.WaitForAssertion(() => page.Find("[data-abom-run-scope]"));

        AssertTipped(page);
    }

    [Fact]
    public void EveryControlOnTheRunsTabSaysWhatItIsFor()
    {
        var page = Arrive("runs", "[data-abom-run-summary]");

        AssertTipped(page);
    }

    [Fact]
    public void EveryControlOnTheSuitesTabSaysWhatItIsFor()
    {
        var page = Arrive("suites", ".abom-tier-head");

        AssertTipped(page);
    }

    /// <summary>
    /// The four parts and the way into a request say what each is for, not where it leads: a tip
    /// reading "Switch to the Runs tab" is the label again.
    /// </summary>
    [Fact]
    public void TheTabsAndNewRequestSayWhatEachIsFor()
    {
        var page = Arrive("requests", "[data-abom-requests-table]");

        Assert.Equal("Ask for a new CodeQL query, or a fix to one, from examples.", TipOf(page, "[data-abom-tab='requests']"));
        Assert.Equal("Every query version ABOM holds, its stage, and where it ran.", TipOf(page, "[data-abom-tab='catalog']"));
        Assert.Equal("Query runs over a scope, and what each found.", TipOf(page, "[data-abom-tab='runs']"));
        Assert.Equal("Choose which query suites each crawl tier runs.", TipOf(page, "[data-abom-tab='suites']"));
        Assert.Equal("Ask for a new or improved CodeQL query for GitHub Actions code.", TipOf(page, "[data-abom-request-new]"));
    }

    private static string? TipOf(IRenderedComponent<CodeQL> page, string selector) =>
        page.Find(selector).Closest("[data-abom-tip]")?.GetAttribute("data-abom-tip");

    private static void AssertTipped(IRenderedComponent<CodeQL> page)
    {
        var controls = page.FindAll(Controls)
            .Where(c => c.Closest(Framework) is null)
            // A select's own parts are described with the select.
            .Where(c => c.ParentElement?.Closest(Controls) is null)
            .ToList();
        var problems = new List<string>();
        foreach (var control in controls)
        {
            var label = Label(control);
            var tip = control.Closest("[data-abom-tip]")?.GetAttribute("data-abom-tip");
            if (string.IsNullOrWhiteSpace(tip))
            {
                problems.Add($"'{label}' has no tip");
            }
            else if (Words(tip) == Words(label))
            {
                problems.Add($"'{label}' has a tip that restates its label");
            }
            if (control.Closest("[title]") is { } titled)
            {
                problems.Add($"'{label}' also shows a native title: {titled.GetAttribute("title")}");
            }
        }

        // A walk that found nothing to check would pass against an empty page.
        Assert.True(controls.Count >= 5, $"only {controls.Count} controls were checked");
        Assert.True(problems.Count == 0, string.Join("\n", problems));
    }

    private static string Label(IElement control)
    {
        var aria = control.GetAttribute("aria-label");
        var text = control.TextContent.Trim();
        return !string.IsNullOrWhiteSpace(aria) ? aria
            : text.Length > 0 ? (text.Length > 60 ? text[..60] : text)
            : control.ClassName ?? control.TagName;
    }

    private static string Words(string text) =>
        new(text.ToLowerInvariant().Where(char.IsLetterOrDigit).ToArray());
}
