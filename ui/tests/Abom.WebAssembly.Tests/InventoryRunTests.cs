using Abom.WebAssembly.Models;
using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>Repositories are narrowed to on the Inventory, so a run is chosen there.</summary>
public class InventoryRunTests : AbomTestContext
{
    private const string Repositories = """
        {"items":[
          {"fullName":"octo-org/billing","origin":"seed","workflows":4,"dependencies":37,"findings":9,"exposures":3,"directExposures":1},
          {"fullName":"octo-org/clean","origin":"seed","workflows":1,"dependencies":2,"findings":0,"exposures":0,"directExposures":0}],
         "total":2}
        """;

    private const string Catalog = """
        {"queries":[
          {"queryId":"acme/self-hosted-runner","queryVersion":"9b5327ab01d7","pack":"abom/custom-queries",
           "source":"custom","discovery":false,"lane":false,"stage":""},
          {"queryId":"abom/edges","queryVersion":"f4b6","pack":"advanced-security/abom-actions-graph-extraction-src",
           "source":"abom","discovery":true,"lane":false,"stage":""}],
         "stages":["dev"]}
        """;

    private const string Created = """
        {"rowId":12,"predicate":"repositories=octo-org%2Fclean","mode":"regular","requestedBy":"operator",
         "state":"pending","createdAt":"2026-09-22T16:00:00Z","finishedAt":"","results":0,
         "queries":[{"queryId":"acme/self-hosted-runner","queryVersion":"9b5327ab01d7"}],
         "members":[],"tally":{"pending":1}}
        """;

    public InventoryRunTests()
    {
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/repositories", Repositories);
        Answer("api/queries", Catalog);
        Answer("api/accounts", """{"authenticated":true,"login":"operator","accounts":[]}""");
        Answer("api/runs/12/start", Created);
        Answer("api/runs", Created);
    }

    private IRenderedComponent<Pages.Inventory> Arrive()
    {
        Services.GetRequiredService<RunDraft>().Set([
            new AnalysisQueryRef { QueryId = "acme/self-hosted-runner", QueryVersion = "9b5327ab01d7" },
        ]);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/inventory?unit=repositories");
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-run-choose]")));
        return page;
    }

    [Fact]
    public void ArrivingWithAVersionToRunNamesItAndWaitsForRepositories()
    {
        var page = Arrive();

        Assert.Contains("acme/self-hosted-runner", page.Find("[data-abom-run-draft]").TextContent);
        Assert.True(page.Find("[data-abom-run-go]").HasAttribute("disabled"));
    }

    /// <summary>Sent by name: a repository with no finding is as runnable as any.</summary>
    [Fact]
    public void RunningSendsTheChosenRepositoriesAndStartsTheRun()
    {
        var page = Arrive();

        page.Find("[data-abom-run-choose='octo-org/clean'] input").Change(true);
        page.Find("[data-abom-run-go]").Click();

        page.WaitForAssertion(() =>
        {
            var created = Assert.Single(Sent, r => r.Method == "POST" && r.Url.Contains("api/runs?"));
            Assert.Contains("repositories=octo-org%2Fclean", created.Url);
            Assert.Contains("acme/self-hosted-runner", created.Body);
            Assert.Contains(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/runs/12/start"));
        });
        var at = Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri;
        Assert.EndsWith("/codeql?tab=runs&run=12", at);
        Assert.Empty(Services.GetRequiredService<RunDraft>().Queries);
    }

    /// <summary>
    /// Predicting is the same draft sent as a prediction, so what its queries would report is held
    /// apart rather than stored as findings. A plain run must not be sent as one.
    /// </summary>
    [Fact]
    public void PredictingSendsTheDraftAsAPredictionAndStartsIt()
    {
        var page = Arrive();
        Assert.True(page.Find("[data-abom-run-predict]").HasAttribute("disabled"));
        Assert.False(string.IsNullOrWhiteSpace(page.Find("[data-abom-run-predict-reason]").GetAttribute("title")));

        page.Find("[data-abom-run-choose='octo-org/clean'] input").Change(true);
        page.Find("[data-abom-run-predict]").Click();

        page.WaitForAssertion(() =>
        {
            var created = Assert.Single(Sent, r => r.Method == "POST" && r.Url.Contains("api/runs?"));
            Assert.Contains("\"prediction\":true", created.Body);
            Assert.Contains("repositories=octo-org%2Fclean", created.Url);
            Assert.Contains(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/runs/12/start"));
        });
        Assert.EndsWith("/codeql?tab=runs&run=12",
            Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri);
    }

    [Fact]
    public void RunningIsNotSentAsAPrediction()
    {
        var page = Arrive();

        page.Find("[data-abom-run-choose='octo-org/clean'] input").Change(true);
        page.Find("[data-abom-run-go]").Click();

        page.WaitForAssertion(() =>
        {
            var created = Assert.Single(Sent, r => r.Method == "POST" && r.Url.Contains("api/runs?"));
            Assert.Contains("\"prediction\":false", created.Body);
        });
    }

    /// <summary>The crawl's own queries build the graph; there is nothing in them to run on demand.</summary>
    [Fact]
    public void OnlyQueriesThatReportFindingsAreOffered()
    {
        var page = Arrive();

        Assert.DoesNotContain("abom/edges", page.Find("[data-abom-run-draft]").TextContent);
    }

    [Fact]
    public void ChoosingRepositoriesIsOfferedWithoutArrivingFromAQuery()
    {
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!
            .NavigateTo("/inventory?unit=repositories");
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => page.Find("[data-abom-action='run-queries']"));
        Assert.Empty(page.FindAll("[data-abom-run-choose]"));

        page.Find("[data-abom-action='run-queries']").Click();

        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-run-choose]")));
    }

    private IRenderedComponent<Pages.Inventory> ArriveAt(string address)
    {
        Services.GetRequiredService<RunDraft>().Set([
            new AnalysisQueryRef { QueryId = "acme/self-hosted-runner", QueryVersion = "9b5327ab01d7" },
        ]);
        Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.NavigateTo(address);
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-run-draft]")));
        return page;
    }

    /// <summary>
    /// A selection larger than a page is the scope itself: sent as its predicate, so the server
    /// resolves it the way the Repositories unit lists it, rather than as names chosen page by page.
    /// </summary>
    [Fact]
    public void ChoosingAllInTheScopeSendsTheScopeRatherThanItsNames()
    {
        var page = ArriveAt("/inventory?unit=repositories&scope=owned");
        page.WaitForAssertion(() => page.Find("[data-abom-run-all] input"));

        page.Find("[data-abom-run-all] input").Change(true);
        page.WaitForAssertion(() =>
            Assert.Contains("2 repositories in this scope", page.Find("[data-abom-run-chosen]").TextContent));
        page.Find("[data-abom-run-go]").Click();

        page.WaitForAssertion(() =>
        {
            var created = Assert.Single(Sent, r => r.Method == "POST" && r.Url.Contains("api/runs?"));
            Assert.EndsWith("api/runs?scope=owned", created.Url);
        });
    }

    /// <summary>An empty scope names no repository, so there is nothing for "all" to send.</summary>
    [Fact]
    public void ChoosingAllIsRefusedUntilSomethingNarrowsTheScope()
    {
        var page = ArriveAt("/inventory?unit=repositories");
        page.WaitForAssertion(() => page.Find("[data-abom-run-all] input"));

        Assert.True(page.Find("[data-abom-run-all] input").HasAttribute("disabled"));
        Assert.Contains("Narrow", page.Find("[data-abom-run-all]").GetAttribute("title"));
    }

    /// <summary>N actions are not N repositories, so "all" is offered only where the rows are.</summary>
    [Fact]
    public void ChoosingAllIsOfferedOnlyWhereTheRowsAreRepositories()
    {
        Answer("api/actions", """{"items":[],"total":0,"elsewhere":[]}""");
        var page = ArriveAt("/inventory?unit=dependencies&scope=owned");

        Assert.Empty(page.FindAll("[data-abom-run-all]"));
    }

    /// <summary>
    /// The size is stated before anything is spent: how many repositories the server resolved the
    /// draft to and how many already have a database, from the resolution the run itself uses.
    /// </summary>
    [Fact]
    public void TheDraftStatesWhatTheRunWouldCoverBeforeItIsPressed()
    {
        Answer("api/databases", """
            {"databases":[
              {"repository":"octo-org/billing","commit":"a1","sourceFilter":"f","mode":"regular",
               "status":"retained","path":"/db/billing","bytes":10},
              {"repository":"octo-org/clean","commit":"b2","sourceFilter":"f","mode":"regular",
               "status":"buildable","path":"","bytes":0}],
             "gaps":[{"repository":"octo-org/new","reason":"no regular analysis on record"}],
             "mode":"regular","unresolved":""}
            """);
        var page = ArriveAt("/inventory?unit=repositories&scope=owned");
        page.WaitForAssertion(() => page.Find("[data-abom-run-all] input"));

        page.Find("[data-abom-run-all] input").Change(true);

        page.WaitForAssertion(() =>
        {
            var size = page.Find("[data-abom-run-size]").TextContent;
            Assert.Contains("3 repositories", size);
            Assert.Contains("1 with a database held", size);
            Assert.Contains("2 without one", size);
        });
        Assert.Contains(Sent, r => r.Method == "GET" && r.Url.EndsWith("api/databases?scope=owned"));
    }
}
