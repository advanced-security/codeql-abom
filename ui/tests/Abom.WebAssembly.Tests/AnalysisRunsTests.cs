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

    /// <summary>
    /// While a run is under way, what it has still to do comes first, and a time remaining is the
    /// server's, taken from what this start finished.
    /// </summary>
    [Fact]
    public void ARunUnderWayLeadsWithWhatIsLeft()
    {
        var cut = Render(RunJson(3, "running").Replace("\"members\":[]",
            "\"members\":[],\"left\":{\"members\":28,\"finished\":2,\"remainingSeconds\":1680}"));

        var tally = cut.Find("[data-abom-run-tally]").TextContent.Trim();
        Assert.StartsWith("28 still to do", tally);
        Assert.Contains("about 28 min left", tally);
    }

    /// <summary>
    /// A run asked for starts a beat later, so the list watches a pending run as closely as a
    /// running one: otherwise what it says is left lags what the run's own row says.
    /// </summary>
    [Fact]
    public void APendingRunIsWatchedAsCloselyAsARunningOne()
    {
        var cut = Render(RunJson(4, "pending"), RunJson(2, "complete"));

        Assert.Equal(TimeSpan.FromSeconds(5), cut.Instance.Beat);
    }

    /// <summary>Before anything of a run has finished there is no pace, so there is no time.</summary>
    [Fact]
    public void ARunNothingOfHasFinishedStatesNoTimeRemaining()
    {
        var cut = Render(
            RunJson(4, "pending").Replace("\"members\":[]",
                "\"members\":[],\"left\":{\"members\":3,\"finished\":0,\"remainingSeconds\":null}"),
            RunJson(2, "complete"));

        var tallies = cut.FindAll("[data-abom-run-tally]").Select(e => e.TextContent.Trim()).ToList();
        Assert.StartsWith("3 still to do", tallies[0]);
        Assert.DoesNotContain("left", tallies[0]);
        Assert.DoesNotContain("still to do", tallies[1]);
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

    /// <summary>
    /// A run that could not start says why where it is listed. Left pending, it read as a run about
    /// to start while the reason reached only the server's log.
    /// </summary>
    [Fact]
    public void ARefusedRunSaysWhyAndCanBeTriedAgain()
    {
        const string why = "no suite this run can see offers acme/gone";
        var cut = Render(
            RunJson(7, "refused", results: 0).Replace("\"finishedAt\":\"\"",
                $"\"finishedAt\":\"2026-09-22T16:00:05Z\",\"refusal\":\"{why}\""),
            RunJson(6, "complete"));

        Assert.Equal("refused", cut.Find("[data-abom-run-state=refused]").TextContent.Trim());
        var refused = Assert.Single(cut.FindAll("[data-abom-run-refused]"));
        Assert.Contains("acme/gone", refused.TextContent);
        Assert.Equal(why, refused.GetAttribute("title"));
        Assert.Equal(2, cut.FindAll("[data-abom-run-again]").Count);
        var named = cut.FindAll("[data-abom-run-codeql]").Select(e => e.TextContent.Trim()).ToList();
        Assert.Equal(["CodeQL chosen when it runs", "CodeQL not recorded"], named);
    }

    [Fact]
    public void ARunsScopeIsReadAsTheRepositoriesItNamed()
    {
        var cut = Render(RunJson(1, "complete"));

        Assert.Contains("octo/app, octo/tool", cut.Find("[data-abom-run-scope]").TextContent);
        Assert.Empty(cut.FindAll("[data-abom-run-scope-name]"));
    }

    /// <summary>
    /// A run chosen by a named scope says which, beside the question it resolved to; a name no
    /// longer kept still reads as the name it was chosen by.
    /// </summary>
    [Fact]
    public void ARunChosenByANamedScopeNamesIt()
    {
        Answer("api/views", """
            [{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true,"refuses":{}}]
            """);
        var cut = Render(
            RunJson(2, "complete", predicate: "scope=owned").Replace("\"members\":[]",
                "\"members\":[],\"scopeName\":\"owned\""),
            RunJson(1, "complete", predicate: "q=octo").Replace("\"members\":[]",
                "\"members\":[],\"scopeName\":\"gone\""));

        cut.WaitForAssertion(() =>
        {
            var names = cut.FindAll("[data-abom-run-scope-name]").Select(e => e.TextContent.Trim()).ToList();
            Assert.Equal(["What you own", "gone"], names);
        });
        Assert.Contains("scope: owned", cut.FindAll("[data-abom-run-scope]")[0].TextContent);
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
