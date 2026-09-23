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
          {"queryId":"acme/a","queryVersion":"v2","origin":"user","working":true,"steps":[]},
          {"queryId":"acme/a","queryVersion":"v1","origin":"user","working":false,"steps":[]},
          {"queryId":"actions/unpinned-tag","queryVersion":"u1","origin":"upstream","steps":[]},
          {"queryId":"abom/edges","queryVersion":"e1","origin":"abom","discovery":true,"steps":[]},
          {"queryId":"actions/unpinned-tag actions/untrusted-checkout","queryVersion":"",
           "origin":"upstream","lane":true,"steps":[]}
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
            {"step":"tests","state":"pending","detail":"never run for this version"}]}],
         "kinds":["","missed","false-alarm","both"],"statuses":["open","in-progress","done","declined"]}
        """;

    /// <summary>
    /// A target is a selection, never text: what is on offer is every query the catalog holds that
    /// reports findings, once each, and nothing that builds the graph or ran as a whole suite.
    /// </summary>
    [Fact]
    public void TheQueriesOnOfferAreTheOnesTheCatalogHolds()
    {
        var catalog = System.Text.Json.JsonSerializer.Deserialize<Models.QueryCatalog>(Catalog,
            new System.Text.Json.JsonSerializerOptions(System.Text.Json.JsonSerializerDefaults.Web))!;

        var offered = Shared.QueryRequests.Choices(catalog);

        Assert.Equal([("acme/a", "custom"), ("actions/unpinned-tag", "upstream")], offered);
    }

    [Fact]
    public void TheQueryAFormIsAboutIsChosenRatherThanTyped()
    {
        var cut = Render(None);
        cut.Find("[data-abom-request-new]").Click();

        var field = cut.Find("[data-abom-request-query]");
        Assert.NotNull(field.QuerySelector(".mud-select"));
        Assert.Null(field.QuerySelector("textarea"));
        Assert.Contains("agent", field.TextContent, StringComparison.OrdinalIgnoreCase);
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
        Assert.Empty(strip.QuerySelectorAll("button"));
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

    private static void Write(IRenderedComponent<Shared.QueryRequests> cut, string field, string text) =>
        cut.Find($"[data-abom-request-{field}] textarea").Input(text);

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
}
