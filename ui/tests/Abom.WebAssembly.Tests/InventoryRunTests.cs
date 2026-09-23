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
           "origin":"user","discovery":false,"lane":false,"stage":""},
          {"queryId":"abom/edges","queryVersion":"f4b6","pack":"advanced-security/abom-actions-graph-extraction-src",
           "origin":"abom","discovery":true,"lane":false,"stage":""}],
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
}
