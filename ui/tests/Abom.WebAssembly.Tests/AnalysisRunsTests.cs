using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>Each run offers the control its state allows; running again is not starting.</summary>
public class AnalysisRunsTests : AbomTestContext
{
    private static string RunJson(long id, string state, int results = 4,
        string predicate = "repositories=octo%2Fapp,octo%2Ftool") => $$$"""
        {"rowId":{{{id}}},"predicate":"{{{predicate}}}","mode":"regular",
         "requestedBy":"operator","state":"{{{state}}}","createdAt":"2026-09-22T16:00:00Z",
         "finishedAt":"","results":{{{results}}},
         "queries":[{"queryId":"acme/rule","queryVersion":"v1aabbccddee"}],
         "members":[],"tally":{"succeeded":1,"no_database":1}}
        """;

    private IRenderedComponent<Shared.AnalysisRuns> Render(params string[] runs)
    {
        Answer("api/runs", $$"""{"runs":[{{string.Join(",", runs)}}]}""");
        var cut = Render<Shared.AnalysisRuns>();
        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-run-id]")));
        return cut;
    }

    [Fact]
    public void AListedRunSaysHowMuchItFound()
    {
        var cut = Render(RunJson(1, "complete"));

        Assert.Equal("4", cut.Find("[data-abom-run-results]").TextContent.Trim());
    }

    [Fact]
    public void AFinishedRunOffersToRunAgainRatherThanToStart()
    {
        var cut = Render(RunJson(2, "complete"), RunJson(1, "pending"));

        Assert.Single(cut.FindAll("[data-abom-run-again]"));
        Assert.Single(cut.FindAll("[data-abom-run-start]"));
        Assert.NotEqual(
            cut.Find("[data-abom-run-again]").GetAttribute("aria-label"),
            cut.Find("[data-abom-run-start]").GetAttribute("aria-label"));
    }

    /// <summary>A run the server stopped under was neither finished nor canceled.</summary>
    [Fact]
    public void AnInterruptedRunSaysSoAndCanBeStartedAgain()
    {
        var cut = Render(RunJson(5, "interrupted"));

        Assert.Equal("interrupted", cut.Find("[data-abom-run-state]").TextContent.Trim());
        Assert.NotEmpty(cut.FindAll("[data-abom-run-again]"));
    }

    [Fact]
    public void ARunsScopeIsReadAsTheRepositoriesItNamed()
    {
        var cut = Render(RunJson(1, "complete"));

        Assert.Contains("octo/app, octo/tool", cut.Find("[data-abom-run-scope]").TextContent);
    }

    /// <summary>
    /// A run records its question in canonical form, where a list's commas are escaped. It still
    /// reads as the repositories it named, and a long list says how many it left unsaid.
    /// </summary>
    [Fact]
    public void ARecordedQuestionReadsAsTheRepositoriesItNamed()
    {
        var cut = Render(RunJson(1, "complete",
            predicate: "repositories=octo%2Fapp%2Cocto%2Fcli%2Cocto%2Ftool%2Cocto%2Fweb"));

        Assert.Contains("octo/app, octo/cli, octo/tool and 1 more",
            cut.Find("[data-abom-run-scope]").TextContent);
    }

    /// <summary>Each query a run names opens its own row in the catalog, version included.</summary>
    [Fact]
    public void AQueryARunNamesOpensItsCatalogRow()
    {
        var cut = Render(RunJson(1, "complete"));

        var link = cut.Find("[data-abom-run-query-link]");
        Assert.Equal("/codeql?tab=catalog&query=acme%2Frule", link.GetAttribute("href"));
        Assert.Equal("acme/rule", link.TextContent.Trim());
    }

    [Fact]
    public void OpeningARunShowsWhatItFoundUnderIt()
    {
        Answer("api/runs/1", RunJson(1, "complete"));
        var cut = Render(RunJson(1, "complete"));

        cut.Find("[data-abom-run-detail]").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-run-summary]")));
    }

    /// <summary>
    /// A result is CodeQL's as much as the query's, so a run names the CodeQL it ran under, and one
    /// recorded before that was kept says so rather than showing nothing.
    /// </summary>
    [Fact]
    public void ARunNamesTheCodeQLItRanUnder()
    {
        var cut = Render(RunJson(2, "complete").Replace("\"mode\":\"regular\"", "\"mode\":\"regular\",\"cliVersion\":\"2.27.1\""),
            RunJson(1, "complete"));

        var named = cut.FindAll("[data-abom-run-codeql]").Select(e => e.TextContent.Trim()).ToList();
        Assert.Equal(["CodeQL 2.27.1", "CodeQL not recorded"], named);
    }

    [Fact]
    public void NoRunsSaysWhereOneIsStarted()
    {
        Answer("api/runs", """{"runs":[]}""");
        var cut = Render<Shared.AnalysisRuns>();

        cut.WaitForAssertion(() => Assert.Contains("Inventory", cut.Find("[data-abom-runs-empty]").TextContent));
    }
}
