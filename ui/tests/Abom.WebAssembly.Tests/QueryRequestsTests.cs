using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>A request names what to catch and shows code on both sides; both are required.</summary>
public class QueryRequestsTests : AbomTestContext
{
    private const string SignedIn = """{"authenticated":true,"login":"operator","accounts":[]}""";
    private const string SignedOut = """{"authenticated":false,"login":"","accounts":[]}""";

    private const string None = """
        {"requests":[],"kinds":["","missed","false-alarm","both"],
         "statuses":["open","in-progress","done","declined"]}
        """;

    private const string One = """
        {"requests":[{"rowId":7,"description":"flag a self-hosted runner\nreached from pull_request",
          "compliant":"runs-on: ubuntu-latest","nonCompliant":"runs-on: self-hosted",
          "queryId":"","kind":"missed","status":"open","requestedBy":"operator",
          "createdAt":"2026-09-22T16:00:00Z","updatedAt":"2026-09-22T16:00:00Z"}],
         "kinds":["","missed","false-alarm","both"],"statuses":["open","in-progress","done","declined"]}
        """;

    private IRenderedComponent<Shared.QueryRequests> Render(string list, string identity = SignedIn)
    {
        Answer("api/query-requests", list);
        Answer("api/accounts", identity);
        Answer("api/queries", Catalog);
        var cut = Render<Shared.QueryRequests>();
        cut.WaitForAssertion(() => cut.Find("[data-abom-requests]"));
        return cut;
    }

    private const string Catalog = """
        {"queries":[
          {"queryId":"acme/a","queryVersion":"v2","source":"custom","working":true,"steps":[]},
          {"queryId":"acme/a","queryVersion":"v1","source":"custom","working":false,"steps":[]},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","source":"upstream","steps":[]},
          {"queryId":"abom/edges","queryVersion":"e1","source":"abom","discovery":true,"steps":[]},
          {"queryId":"actions/unpinned-tag actions/untrusted-checkout","queryVersion":"",
           "source":"upstream","lane":true,"steps":[]}
        ],"stages":["dev","test"]}
        """;

    private const string Targeted = """
        {"requests":[{"rowId":8,"description":"stop flagging runners we own",
          "compliant":"runs-on: [self-hosted, acme]","nonCompliant":"runs-on: self-hosted",
          "queryId":"acme/a","targetSource":"operator","kind":"false-alarm","status":"open",
          "requestedBy":"operator","createdAt":"2026-09-23T10:00:00Z","updatedAt":"2026-09-23T10:00:00Z",
          "sourceRunId":0,"evidence":[],
          "steps":[
            {"step":"target","state":"passing","detail":"acme/a, selected by you"},
            {"step":"help","state":"pending","detail":"no src/A.md beside the query"},
            {"step":"expected","state":"passing","detail":"test/A/A.expected: 1 line"},
            {"step":"database","state":"pending","detail":"test/A has not been extracted"},
            {"step":"ast","state":"pending","detail":"no PrintAST run is recorded"},
            {"step":"compiles","state":"pending","detail":"not compiled for this version"},
            {"step":"tests","state":"pending","detail":"never run for this version"}],
          "outcome":{"state":"unfinished","headline":"Unfinished: Query help",
            "detail":"no src/A.md beside the query","step":"help"}}],
         "kinds":["","missed","false-alarm","both"],"statuses":["open","in-progress","done","declined"]}
        """;

    /// <summary>
    /// Seven marks say where each step stands, and none says where the request does: that is one
    /// answer, read first, before the marks it was read from.
    /// </summary>
    [Fact]
    public void ARequestSaysWhereItStandsBeforeItsSteps()
    {
        var cut = Render(Targeted);

        var chip = cut.Find("[data-abom-request-outcome]");
        Assert.Equal("unfinished", chip.GetAttribute("data-abom-request-outcome"));
        Assert.Contains("Unfinished: Query help", chip.TextContent);
        var cell = chip.Closest("td")!;
        Assert.True(cell.InnerHtml.IndexOf("data-abom-request-outcome", StringComparison.Ordinal)
            < cell.InnerHtml.IndexOf("data-abom-steps", StringComparison.Ordinal));
    }

    /// <summary>An opened request says where it stands and why above the steps it was read from.</summary>
    [Fact]
    public void AnOpenedRequestSaysWhereItStandsAndWhyAboveItsSteps()
    {
        Answer("api/query-requests", Targeted);
        Answer("api/accounts", SignedIn);
        Answer("api/queries", Catalog);

        var cut = Render<Shared.QueryRequests>(p => p.Add(c => c.FocusRequest, 8L));

        cut.WaitForAssertion(() => cut.Find("[data-abom-request-outcome-detail]"));
        var said = cut.Find("[data-abom-request-outcome-detail]").TextContent;
        Assert.Contains("Unfinished: Query help", said);
        Assert.Contains("no src/A.md beside the query", said);
        var open = cut.Find("[data-abom-request-open='8']").InnerHtml;
        Assert.True(open.IndexOf("data-abom-request-outcome-detail", StringComparison.Ordinal)
            < open.IndexOf("data-abom-request-steps", StringComparison.Ordinal));
    }

    /// <summary>A server that said nothing about where a request stands is not read as ready.</summary>
    [Fact]
    public void ARequestWithNoOutcomeShowsNone()
    {
        var cut = Render(One);

        cut.WaitForAssertion(() => cut.Find("[data-abom-request-title-cell]"));
        Assert.Empty(cut.FindAll("[data-abom-request-outcome]"));
    }

    /// <summary>
    /// A session that ends changes where its request stands, so the row is read again then, and the
    /// reader is left where they were.
    /// </summary>
    [Fact]
    public void ASessionThatEndsRefreshesWhereItsRequestStands()
    {
        const string running = """
            {"sessions":[{"rowId":2,"requestId":8,"state":"running","startedBy":"operator",
              "model":"m","startedAt":"2026-09-24T02:20:25Z","endedAt":"","detail":"","events":1,"refused":0}],
             "inFlight":[]}
            """;
        Answer("api/query-requests/8/sessions", running);
        Answer("api/query-requests/8/asts", """{"asts":[]}""");
        Answer("api/query-requests", Targeted.Replace("\"state\":\"unfinished\"", "\"state\":\"working\""));
        Answer("api/accounts", SignedIn);
        Answer("api/queries", Catalog);
        var cut = Render<Shared.QueryRequests>(p => p.Add(c => c.FocusRequest, 8L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-session-stop]"));
        Assert.Equal("working", cut.Find("[data-abom-request-outcome]").GetAttribute("data-abom-request-outcome"));

        Answer("api/query-requests/8/sessions", running.Replace("\"running\"", "\"stopped\""));
        Answer("api/query-requests/8/sessions/stop", "{}");
        Answer("api/query-requests", Targeted);
        cut.Find("[data-abom-session-stop]").Click();

        cut.WaitForAssertion(() => Assert.Equal("unfinished",
            cut.Find("[data-abom-request-outcome]").GetAttribute("data-abom-request-outcome")));
        Assert.NotNull(cut.Find("[data-abom-request-open='8']"));
    }

    /// <summary>
    /// What is offered to pick is what a request can improve: the working copies in the operator's
    /// pack, once each, and nothing upstream, nothing that builds the graph and nothing that ran as
    /// a whole suite. Any other id is typed, and asks for a new query.
    /// </summary>
    [Fact]
    public void TheQueriesOnOfferAreTheOnesThePackHolds()
    {
        var catalog = System.Text.Json.JsonSerializer.Deserialize<Models.QueryCatalog>(Catalog,
            new System.Text.Json.JsonSerializerOptions(System.Text.Json.JsonSerializerDefaults.Web))!;

        var offered = Shared.QueryRequestForm.Choices(catalog);

        Assert.Equal([("acme/a", "custom")], offered);
    }

    /// <summary>
    /// Where a request stands is read without opening anything, and who chose its target is part
    /// of what the first mark says.
    /// </summary>
    [Fact]
    public void ARequestShowsItsMarksWithTheTargetFirst()
    {
        var cut = Render(Targeted);

        var strip = cut.Find("[data-abom-steps]");
        Assert.Equal(7, strip.QuerySelectorAll("[data-abom-step]").Length);
        Assert.Contains("selected by you",
            cut.Find("[data-abom-step=target]").GetAttribute("aria-label") ?? "");
        Assert.Empty(strip.QuerySelectorAll("button[data-abom-query-test], button[data-abom-query-print]"));
    }

    /// <summary>
    /// A request's marks open what backs them both in its row and in its detail, and the detail's
    /// marks carry the strip's tooltips.
    /// </summary>
    [Fact]
    public void ARequestsMarksOpenWhatBacksThemInTheRowAndInItsDetail()
    {
        Answer("api/queries/step", """
            {"step":"expected","files":[{"path":"test/A/A.expected","body":"| a job | flagged |","truncated":false}],"absent":""}
            """);
        var dialogs = Render<MudBlazor.MudDialogProvider>();
        var cut = Render(Targeted);

        cut.Find("[data-abom-steps] button[data-abom-step-view][data-abom-step=expected]").Click();
        dialogs.WaitForAssertion(() => Assert.Contains("| a job | flagged |", dialogs.Markup));
        dialogs.Find("button[data-abom-step-close]").Click();
        dialogs.WaitForAssertion(() => Assert.DoesNotContain("| a job | flagged |", dialogs.Markup));

        cut.Find("[data-abom-request-detail]").Click();
        cut.WaitForAssertion(() => cut.Find("[data-abom-request-steps]"));
        Assert.All(cut.FindAll("[data-abom-request-step]"),
            line => Assert.Single(line.QuerySelectorAll(".mud-tooltip-root")));
        cut.Find("[data-abom-request-step=expected] button[data-abom-step-view]").Click();
        dialogs.WaitForAssertion(() => Assert.Contains("| a job | flagged |", dialogs.Markup));
        Assert.Equal(2, Sent.Count(r => r.Url.Contains("api/queries/step?query=acme%2Fa&step=expected")));
    }

    /// <summary>
    /// A request has an address of its own: named on arrival it opens, and its detail reads each
    /// step with its evidence, and the run it was asked from, without hovering over a mark.
    /// </summary>
    [Fact]
    public void ARequestNamedOnArrivalOpensWithEachStepsEvidenceAndItsRun()
    {
        Answer("api/query-requests", Targeted.Replace("\"sourceRunId\":0", "\"sourceRunId\":4"));
        Answer("api/accounts", SignedIn);
        Answer("api/queries", Catalog);

        var cut = Render<Shared.QueryRequests>(p => p.Add(c => c.FocusRequest, 8L));

        cut.WaitForAssertion(() => cut.Find("[data-abom-request-steps]"));
        Assert.Equal(7, cut.FindAll("[data-abom-request-step]").Count);
        Assert.Contains("no src/A.md beside the query", cut.Find("[data-abom-request-step=help]").TextContent);
        Assert.Equal("/codeql?tab=runs&run=4", cut.Find("[data-abom-request-source-run]").GetAttribute("href"));
    }

    [Fact]
    public void ARequestsQueryOpensItsRowInTheCatalog()
    {
        var cut = Render(Targeted);

        var link = cut.Find("[data-abom-request-query-link]");
        Assert.Equal("/codeql?tab=catalog&query=acme%2Fa", link.GetAttribute("href"));
    }

    /// <summary>An edit keeps the query the request is about unless somebody changes it.</summary>
    [Fact]
    public void EditingARequestKeepsTheQueryItIsAbout()
    {
        var cut = Render(Targeted);
        cut.Find("[data-abom-request-edit]").Click();
        Write(cut, "description", "stop flagging runners we own, on any trigger");

        cut.Find("[data-abom-request-save]").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "PUT" && r.Url.EndsWith("api/query-requests/8"));
            Assert.Contains("\"queryId\":\"acme/a\"", sent.Body);
        });
    }

    private static void Write(IRenderedComponent<Shared.QueryRequests> cut, string field, string text)
    {
        var selector = $"[data-abom-request-{field}] textarea";
        if (cut.FindAll(selector).Count == 0)
        {
            cut.Find($"[data-abom-request-{field}] [data-abom-example-add]").Click();
        }
        cut.Find(selector).Input(text);
    }

    private static void Name(IRenderedComponent<Shared.QueryRequests> cut, string title) =>
        cut.Find("[data-abom-request-title] input").Input(title);

    [Fact]
    public void NoRequestsSaysWhatARequestIsFor()
    {
        var cut = Render(None);

        Assert.Contains("COMPLIANT", cut.Find("[data-abom-requests-empty]").TextContent);
    }

    [Fact]
    public void TheExamplesAreLabeledTheWayAQueryTestMarksThem()
    {
        var cut = Render(None);
        cut.Find("[data-abom-request-new]").Click();

        Assert.Contains("COMPLIANT", cut.Find("[data-abom-request-compliant]").TextContent);
        Assert.Contains("NON_COMPLIANT", cut.Find("[data-abom-request-noncompliant]").TextContent);
    }

    [Fact]
    public void ARequestCannotBeSavedWithoutADescriptionAndBothExamples()
    {
        var cut = Render(None);
        cut.Find("[data-abom-request-new]").Click();

        Write(cut, "description", "flag a self-hosted runner");
        Write(cut, "compliant", "runs-on: ubuntu-latest");

        Assert.True(cut.Find("[data-abom-request-save]").HasAttribute("disabled"));
    }

    [Fact]
    public void SavingARequestSendsWhatWasWrittenAndWhoWroteIt()
    {
        var cut = Render(None);
        cut.Find("[data-abom-request-new]").Click();
        Name(cut, "Self-hosted runner");
        Write(cut, "description", "flag a self-hosted runner");
        Write(cut, "compliant", "runs-on: ubuntu-latest");
        Write(cut, "noncompliant", "runs-on: self-hosted");

        cut.Find("[data-abom-request-save]").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "POST" && r.Url.Contains("api/query-requests"));
            Assert.Contains("\"nonCompliant\":\"runs-on: self-hosted\"", sent.Body);
            Assert.Contains("\"requestedBy\":\"operator\"", sent.Body);
        });
    }

    [Fact]
    public void OpeningARequestShowsBothExamples()
    {
        var cut = Render(One);

        cut.Find("[data-abom-request-detail]").Click();

        cut.WaitForAssertion(() =>
        {
            Assert.Contains("runs-on: ubuntu-latest", cut.Find("[data-abom-request-shown-compliant]").TextContent);
            Assert.Contains("runs-on: self-hosted", cut.Find("[data-abom-request-shown-noncompliant]").TextContent);
        });
    }

    /// <summary>
    /// A tag a request names opens the findings of every rule declaring exactly that tag, which is
    /// how a CWE it cites is reported on.
    /// </summary>
    [Fact]
    public void ARequestsTagOpensTheFindingsDeclaringIt()
    {
        var cut = Render(One.Replace("\"status\":\"open\",",
            "\"status\":\"open\",\"referenceTags\":[\"external/cwe/cwe-829\"],"));

        cut.Find("[data-abom-request-detail]").Click();

        cut.WaitForAssertion(() => Assert.Equal("/inventory?unit=findings&tags=external%2Fcwe%2Fcwe-829",
            cut.Find("[data-abom-request-shown-tag]").GetAttribute("href")));
    }

    [Fact]
    public void EditingARequestSavesItUnderItsOwnNumber()
    {
        var cut = Render(One);
        cut.Find("[data-abom-request-edit]").Click();
        Write(cut, "description", "flag a self-hosted runner on any trigger");

        cut.Find("[data-abom-request-save]").Click();

        cut.WaitForAssertion(() =>
            Assert.Single(Sent, r => r.Method == "PUT" && r.Url.EndsWith("api/query-requests/7")));
    }

    [Fact]
    public void WithNoIdentityARequestCannotBeFiled()
    {
        var cut = Render(None, SignedOut);

        Assert.True(cut.Find("[data-abom-request-new]").HasAttribute("disabled"));
    }

    /// <summary>
    /// Asked from a run, the form opens with the query, the kind and what was picked already in
    /// it, and the picked results travel with the request when the operator saves it.
    /// </summary>
    [Fact]
    public void ARequestAskedFromARunOpensWithWhatWasPicked()
    {
        Services.GetRequiredService<Services.RequestDraft>().Set(new Models.QueryRequest
        {
            QueryId = "acme/a", Kind = "false-alarm", SourceRunId = 3,
            Evidence = [new Models.RequestEvidence
            {
                RunId = 3, Repository = "octo/app", FindingRowId = 41, Kind = "false-alarm",
            }],
        });
        var cut = Render(None);

        Assert.Contains("1 result picked from run #3", cut.Find("[data-abom-request-evidence]").TextContent);
        Name(cut, "Runners we own");
        Write(cut, "description", "stop flagging runners we own");
        Write(cut, "compliant", "runs-on: [self-hosted, acme]");
        Write(cut, "noncompliant", "runs-on: self-hosted");
        cut.Find("[data-abom-request-save]").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "POST" && r.Url.Contains("api/query-requests"));
            Assert.Contains("\"sourceRunId\":3", sent.Body);
            Assert.Contains("\"findingRowId\":41", sent.Body);
            Assert.Contains("\"queryId\":\"acme/a\"", sent.Body);
        });
        Assert.Null(Services.GetRequiredService<Services.RequestDraft>().Request);
    }

    /// <summary>A delete cannot be taken back, so the first press only asks; nothing is sent until the second.</summary>
    [Fact]
    public void DeletingARequestAsksBeforeAnythingIsSent()
    {
        AnswerWrite("api/query-requests/7", None);
        var cut = Render(One);

        cut.Find("[data-abom-request-delete]").Click();
        Assert.DoesNotContain(Sent, r => r.Method == "DELETE");
        Assert.Contains("#7", cut.Find("[data-abom-request-delete-ask]").TextContent);

        cut.Find("[data-abom-request-delete-confirm]").Click();

        cut.WaitForAssertion(() =>
        {
            Assert.Single(Sent, r => r.Method == "DELETE" && r.Url.EndsWith("api/query-requests/7"));
            cut.Find("[data-abom-requests-empty]");
        });
    }

    [Fact]
    public void ADeleteCanBeCalledOff()
    {
        var cut = Render(One);

        cut.Find("[data-abom-request-delete]").Click();
        cut.Find("[data-abom-request-delete-cancel]").Click();

        Assert.Empty(cut.FindAll("[data-abom-request-delete-ask]"));
        Assert.DoesNotContain(Sent, r => r.Method == "DELETE");
    }

    /// <summary>The server refuses a request an agent has worked, and says so where it was asked.</summary>
    [Fact]
    public void ARefusedDeleteSaysWhy()
    {
        RefuseWrite("api/query-requests/7", System.Net.HttpStatusCode.Conflict,
            "request 7: an agent has worked this request, and what it did is kept; resubmit it as a new request and mark this one declined");
        var cut = Render(One);

        cut.Find("[data-abom-request-delete]").Click();
        cut.Find("[data-abom-request-delete-confirm]").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains("resubmit it as a new request", cut.Find("[data-abom-request-refusal]").TextContent));
        cut.Find("[data-abom-request-title-cell]");
    }

    /// <summary>
    /// Resubmitting opens a copy as a new request: everything written comes along, the id is
    /// written again from the title, and saving files it under a new number.
    /// </summary>
    [Fact]
    public void ResubmittingOpensACopyThatIsFiledAsNew()
    {
        var cut = Render(One.Replace("\"rowId\":7,",
            "\"rowId\":7,\"title\":\"Self-hosted runner on a PR\",\"slug\":\"self-hosted-runner-on-a-pr-pull-request-target-with-secrets-inh\","));

        cut.Find("[data-abom-request-resubmit]").Click();

        Assert.Equal("New CodeQL Query Request", cut.Find("[data-abom-request-heading]").TextContent.Trim());
        Assert.Equal("self-hosted-runner-on-a-pr", cut.Find("[data-abom-request-slug] input").GetAttribute("value"));
        cut.Find("[data-abom-request-save]").Click();
        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/query-requests"));
            Assert.Contains("\"nonCompliant\":\"runs-on: self-hosted\"", sent.Body);
            Assert.Contains("\"slug\":\"self-hosted-runner-on-a-pr\"", sent.Body);
        });
        Assert.DoesNotContain(Sent, r => r.Method == "PUT");
    }

    /// <summary>
    /// A query the agent chose for the original is the agent's decision, not the operator's: the
    /// copy leaves it to the next agent, rather than filing it as the operator's own choice.
    /// </summary>
    [Fact]
    public void ResubmittingLeavesTheAgentsChoiceOfQueryToTheNextAgent()
    {
        var cut = Render(Targeted.Replace("\"targetSource\":\"operator\"", "\"targetSource\":\"agent\""));

        cut.Find("[data-abom-request-resubmit]").Click();

        Assert.Equal("", cut.Find("[data-abom-request-query] input").GetAttribute("value"));
    }

    /// <summary>A query the operator chose comes along with everything else they wrote.</summary>
    [Fact]
    public void ResubmittingKeepsTheOperatorsChoiceOfQuery()
    {
        var cut = Render(Targeted);

        cut.Find("[data-abom-request-resubmit]").Click();

        Assert.Equal("acme/a", cut.Find("[data-abom-request-query] input").GetAttribute("value"));
    }

    private const string CopilotMissing = """
        {"known":true,"ready":false,"checks":[
          {"key":"pack","name":"Query pack","state":"ready","path":"/work/ql/custom","source":"--custom-ql-dir",
           "version":"","reason":"","remedy":"","saved":"","settable":false},
          {"key":"copilot","name":"Copilot CLI","state":"problem","path":"copilot","source":"--copilot-cli",
           "version":"","reason":"copilot is not on PATH","remedy":"set its path in Settings, or start the server with --copilot-cli or COPILOT_CLI_PATH",
           "saved":"","settable":true}]}
        """;

    /// <summary>
    /// What an agent session lacks is on the page the session is started from, before anybody
    /// presses start, with what to do about it.
    /// </summary>
    [Fact]
    public void WhatAnAgentSessionLacksIsShownWhereRequestsAreWorked()
    {
        Answer("api/authoring/readiness", CopilotMissing);
        var cut = Render(One);

        cut.WaitForAssertion(() =>
        {
            var problem = Assert.Single(cut.FindAll("[data-abom-readiness-problem]"));
            Assert.Contains("Copilot CLI", problem.TextContent);
            Assert.Contains("copilot is not on PATH", problem.TextContent);
            Assert.Contains("--copilot-cli", problem.TextContent);
        });
        Assert.Equal("/settings#agent-tools", cut.Find("[data-abom-readiness-settings]").GetAttribute("href"));
    }

    /// <summary>
    /// A server that keeps the tools it started with, such as a Demo, refuses a saved path, so the
    /// panel must not send the reader to Settings to set one.
    /// </summary>
    [Fact]
    public void AServerWithFixedToolsOffersNoPathToSet()
    {
        Answer("api/authoring/readiness", CopilotMissing.Replace("\"settable\":true", "\"settable\":false"));
        var cut = Render(One);

        cut.WaitForAssertion(() => Assert.Single(cut.FindAll("[data-abom-readiness-problem]")));
        Assert.Empty(cut.FindAll("[data-abom-readiness-settings]"));
    }

    [Fact]
    public void NothingIsSaidWhenEverythingASessionNeedsIsThere()
    {
        Answer("api/authoring/readiness", """{"known":true,"ready":true,"checks":[]}""");
        var cut = Render(One);

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/authoring/readiness")));
        Assert.Empty(cut.FindAll("[data-abom-readiness]"));
    }

    /// <summary>Checking again probes each CLI afresh rather than repeating what was last read.</summary>
    [Fact]
    public void CheckingAgainAsksTheServerToProbeAfresh()
    {
        Answer("api/authoring/readiness", CopilotMissing);
        var cut = Render(One);
        cut.WaitForAssertion(() => cut.Find("[data-abom-readiness-recheck]"));

        cut.Find("[data-abom-readiness-recheck]").Click();

        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Url.Contains("api/authoring/readiness?fresh=1")));
    }

    private const string Ready = """{"known":true,"ready":true,"checks":[]}""";

    private static readonly string Working = Targeted.Replace(
        """
        "outcome":{"state":"unfinished","headline":"Unfinished: Query help",
        """,
        """
        "outcome":{"state":"working","headline":"Agent working: Query help","passed":2,"total":7,
        """);

    /// <summary>
    /// An agent is started from the request's own row, and the row itself says so: opening it
    /// would push what the agent is doing below the examples.
    /// </summary>
    [Fact]
    public void AnAgentStartedFromTheRowIsShownWorkingInTheRow()
    {
        Answer("api/authoring/readiness", Ready);
        Answer("api/query-requests/8/sessions", """{"sessions":[],"inFlight":[]}""");
        Answer("api/query-requests/8/asts", """{"asts":[]}""");
        var cut = Render(Targeted);
        Assert.Empty(cut.FindAll("[data-abom-request-working]"));

        Answer("api/query-requests", Working);
        cut.WaitForAssertion(() => Assert.False(cut.Find("[data-abom-request-agent]").HasAttribute("disabled")));
        cut.Find("[data-abom-request-agent]").Click();

        cut.WaitForAssertion(() =>
        {
            Assert.Single(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/query-requests/8/sessions")
                && r.Body.Contains("\"by\":\"operator\""));
            var cell = cut.Find("[data-abom-request-outcome]").Closest("td")!;
            Assert.NotNull(cell.QuerySelector("[data-abom-request-working]"));
            Assert.Contains("Agent working: Query help", cell.TextContent);
            Assert.Contains("2 of 7", cell.TextContent);
        });
        Assert.Empty(cut.FindAll("[data-abom-request-open]"));
    }

    /// <summary>A working agent is stopped from its row, without opening it.</summary>
    [Fact]
    public void AWorkingAgentIsStoppedFromItsRow()
    {
        Answer("api/authoring/readiness", Ready);
        var cut = Render(Working);
        cut.WaitForAssertion(() => cut.Find("[data-abom-request-stop]"));
        Assert.Empty(cut.FindAll("[data-abom-request-agent]"));

        Answer("api/query-requests/8/sessions/stop", "{}");
        Answer("api/query-requests", Targeted);
        cut.Find("[data-abom-request-stop]").Click();

        cut.WaitForAssertion(() =>
        {
            Assert.Single(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/query-requests/8/sessions/stop"));
            Assert.Equal("unfinished", cut.Find("[data-abom-request-outcome]").GetAttribute("data-abom-request-outcome"));
            cut.Find("[data-abom-request-agent]");
        });
        Assert.Empty(cut.FindAll("[data-abom-request-working]"));
    }

    /// <summary>A row the reader opened stays open when the list is read again.</summary>
    [Fact]
    public void AnOpenedRowStaysOpenWhenTheListIsReadAgain()
    {
        Answer("api/authoring/readiness", Ready);
        Answer("api/query-requests/8/sessions", """{"sessions":[],"inFlight":[]}""");
        Answer("api/query-requests/8/asts", """{"asts":[]}""");
        var cut = Render(Working);
        cut.Find("[data-abom-request-detail]").Click();
        cut.WaitForAssertion(() => cut.Find("[data-abom-request-open='8']"));

        Answer("api/query-requests/8/sessions/stop", "{}");
        Answer("api/query-requests", Targeted);
        cut.Find("[data-abom-request-stop]").Click();

        cut.WaitForAssertion(() => cut.Find("[data-abom-request-agent]"));
        Assert.NotNull(cut.Find("[data-abom-request-open='8']"));
    }

    /// <summary>
    /// While an agent works the list is read again closely, so its steps move as it goes; otherwise
    /// on the reader's beat, so a session started elsewhere is still seen.
    /// </summary>
    [Fact]
    public void AWorkingRequestIsWatchedClosely()
    {
        var idle = Render(Targeted);
        Assert.Equal(TimeSpan.FromSeconds(Abom.WebAssembly.Services.FollowService.DefaultSeconds), idle.Instance.Beat);

        var cut = Render(Working);

        Assert.Equal(TimeSpan.FromSeconds(3), cut.Instance.Beat);
    }

    [Fact]
    public void AnAgentIsNotOfferedFromTheRowWhileSomethingItNeedsIsMissing()
    {
        Answer("api/authoring/readiness", CopilotMissing);
        var cut = Render(One);

        cut.WaitForAssertion(() => Assert.True(cut.Find("[data-abom-request-agent]").HasAttribute("disabled")));
        Assert.Contains(cut.FindComponents<Shared.Tip>(), t => t.Instance.Text.Contains("copilot is not on PATH"));
    }

    [Fact]
    public void TheCopyActionSaysItResubmitsAsNew()
    {
        var cut = Render(One);

        Assert.Contains(cut.FindComponents<Shared.Tip>(), t => t.Instance.Text.StartsWith("Resubmit as new"));
    }
}
