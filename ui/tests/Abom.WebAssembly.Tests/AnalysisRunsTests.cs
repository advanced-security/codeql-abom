using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>Each run offers the control its state allows; running again is not starting.</summary>
public class AnalysisRunsTests : AbomTestContext
{
    private static string RunJson(long id, string state, int results = 4) => $$$"""
        {"rowId":{{{id}}},"predicate":"repositories=octo%2Fapp,octo%2Ftool","mode":"regular",
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

    [Fact]
    public void OpeningARunShowsWhatItFoundUnderIt()
    {
        Answer("api/runs/1", RunJson(1, "complete"));
        var cut = Render(RunJson(1, "complete"));

        cut.Find("[data-abom-run-detail]").Click();

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-run-summary]")));
    }

    [Fact]
    public void NoRunsSaysWhereOneIsStarted()
    {
        Answer("api/runs", """{"runs":[]}""");
        var cut = Render<Shared.AnalysisRuns>();

        cut.WaitForAssertion(() => Assert.Contains("Inventory", cut.Find("[data-abom-runs-empty]").TextContent));
    }
}
