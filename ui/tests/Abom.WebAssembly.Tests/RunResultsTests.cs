using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>Per repository and per query, with the repositories where something fired first.</summary>
public class RunResultsTests : AbomTestContext
{
    private const string Run = """
        {"rowId":3,"predicate":"repositories=octo%2Fapp,octo%2Fquiet,octo%2Fnone","mode":"regular",
         "requestedBy":"operator","state":"complete","createdAt":"2026-09-22T16:00:00Z",
         "finishedAt":"2026-09-22T16:04:00Z","results":5,
         "queries":[{"queryId":"acme/rule","queryVersion":"v1"},{"queryId":"acme/other","queryVersion":"v2"}],
         "tally":{"succeeded":2,"no_database":1},
         "members":[
           {"repository":"octo/app","status":"succeeded","counted":true,"results":5,
            "byQuery":{"acme/rule":2,"acme/other":3}},
           {"repository":"octo/quiet","status":"succeeded","counted":true,"results":0,
            "byQuery":{"acme/rule":0,"acme/other":0}},
           {"repository":"octo/none","status":"no_database","detail":"no regular analysis on record",
            "counted":false,"results":0,"byQuery":{}}]}
        """;

    private IRenderedComponent<Shared.RunResults> Render(string? queryId)
    {
        Answer("api/runs/3", Run);
        var cut = Render<Shared.RunResults>(p => p
            .Add(c => c.RunId, 3L)
            .Add(c => c.QueryId, queryId));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));
        return cut;
    }

    private static List<string> Repositories(IRenderedComponent<Shared.RunResults> cut) =>
        cut.FindAll("[data-abom-result-repository]").Select(e => e.TextContent.Trim()).ToList();

    [Fact]
    public void OnlyRepositoriesWithResultsAreListedAndTheRestAreCounted()
    {
        var cut = Render("acme/rule");

        Assert.Equal(["octo/app"], Repositories(cut));
        Assert.Contains("2 repositories", cut.Find("[data-abom-results-hidden]").TextContent);
    }

    [Fact]
    public void TheCountIsThisQuerysNotTheRepositorysTotal()
    {
        var cut = Render("acme/rule");

        Assert.Equal("2", cut.Find("[data-abom-member-results]").TextContent.Trim());
    }

    [Fact]
    public void ShowingEveryRepositoryKeepsANeverAnalyzedOneApartFromAZero()
    {
        var cut = Render("acme/rule");
        cut.Find("[data-abom-results-all] input").Change(true);

        Assert.Equal(["octo/app", "octo/quiet", "octo/none"], Repositories(cut));
        var counts = cut.FindAll("[data-abom-member-results]").Select(e => e.TextContent.Trim()).ToList();
        Assert.Equal(["2", "0"], counts);
        Assert.NotNull(cut.Find("[data-abom-member-uncounted]"));
        Assert.Contains("no regular analysis on record", cut.Markup);
    }

    /// <summary>Opened from the run rather than from a query, every query it carried is a column.</summary>
    [Fact]
    public void ARunOpenedOnItsOwnHasAColumnPerQuery()
    {
        var cut = Render(null);

        var heads = cut.FindAll("[data-abom-result-query]").Select(e => e.GetAttribute("data-abom-result-query")).ToList();
        Assert.Equal(["acme/rule", "acme/other"], heads);
        Assert.Equal(["2", "3"], cut.FindAll("[data-abom-member-results]").Select(e => e.TextContent.Trim()));
    }

    /// <summary>Watched only while running, a just-asked pending run froze on "0 analyzed".</summary>
    [Fact]
    public void ARunOpenedBeforeItStartedKeepsReadingUntilItFinishes()
    {
        Answer("api/runs/3", Run.Replace("\"state\":\"complete\"", "\"state\":\"pending\""));
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 3L).Add(c => c.QueryId, "acme/rule"));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));

        Assert.True(cut.Instance.Watching, "a pending run is not being watched");
    }

    [Fact]
    public void ARunThatHasFinishedIsNoLongerWatched()
    {
        var cut = Render("acme/rule");

        Assert.False(cut.Instance.Watching, "a finished run is still being re-read");
    }

    [Fact]
    public void ARunWhereNothingFiredSaysSoRatherThanShowingAnEmptyTable()
    {
        Answer("api/runs/3", Run.Replace("\"acme/rule\":2", "\"acme/rule\":0"));
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 3L).Add(c => c.QueryId, "acme/rule"));

        cut.WaitForAssertion(() => Assert.NotNull(cut.Find("[data-abom-results-none]")));
        Assert.Empty(cut.FindAll("[data-abom-result-repository]"));
    }

    /// <summary>A total counted before the per-query split is not that query's zero.</summary>
    [Fact]
    public void ARepositoryCountedBeforeResultsWereSplitIsNotReadAsZero()
    {
        Answer("api/runs/3", """
            {"rowId":3,"predicate":"repositories=octo%2Fapp","mode":"regular","requestedBy":"operator",
             "state":"complete","createdAt":"2026-09-22T16:00:00Z","finishedAt":"","results":51,
             "queries":[{"queryId":"acme/rule","queryVersion":"v1"}],"tally":{"succeeded":1},
             "members":[{"repository":"octo/app","status":"succeeded","counted":true,"results":51,"byQuery":{}}]}
            """);
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 3L).Add(c => c.QueryId, "acme/rule"));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));

        Assert.DoesNotContain("found 0", cut.Find("[data-abom-run-summary]").TextContent);
        Assert.Equal(["octo/app"], Repositories(cut));
        Assert.NotNull(cut.Find("[data-abom-member-uncounted]"));
        Assert.Equal("51", cut.Find("[data-abom-member-total]").TextContent.Trim());
    }

    private const string AppFindings = """
        {"repository":"octo/app","evaluated":true,"truncated":false,"findings":[
          {"findingRowId":41,"ruleId":"acme/rule","filePath":".github/workflows/ci.yml","startLine":6,
           "startColumn":5,"message":"This job runs on a self-hosted runner.","severity":"warning"},
          {"findingRowId":42,"ruleId":"acme/rule","filePath":".github/workflows/deploy.yml","startLine":9,
           "startColumn":5,"message":"This job runs on a self-hosted runner.","severity":"warning"}]}
        """;

    private IRenderedComponent<Shared.RunResults> Picking(string? queryId)
    {
        Answer("api/runs/3/results", AppFindings);
        var cut = Render(queryId);
        cut.Find("[data-abom-results-pick]").Click();
        return cut;
    }

    /// <summary>
    /// A result that should not have fired is named, not described: picking it attaches that
    /// finding to a request about the query that reported it, and nothing is filed until the
    /// operator saves the request.
    /// </summary>
    [Fact]
    public void PickingAResultAsksForAChangeToTheQueryThatReportedIt()
    {
        var cut = Picking("acme/rule");
        cut.Find("[data-abom-member-findings='octo/app']").Click();
        cut.WaitForAssertion(() => cut.Find("[data-abom-pick-finding='41'] input").Change(true));

        cut.Find("[data-abom-results-ask]").Click();

        var draft = Services.GetRequiredService<RequestDraft>().Request!;
        Assert.Equal("acme/rule", draft.QueryId);
        Assert.Equal(3, draft.SourceRunId);
        Assert.Equal("false-alarm", draft.Kind);
        var picked = Assert.Single(draft.Evidence);
        Assert.Equal((3L, "octo/app", 41L, "false-alarm"),
            (picked.RunId, picked.Repository, picked.FindingRowId, picked.Kind));
        Assert.EndsWith("/codeql?tab=requests",
            Services.GetService<Bunit.TestDoubles.BunitNavigationManager>()!.Uri);
        Assert.DoesNotContain(Sent, r => r.Method == "POST");
    }

    /// <summary>
    /// A miss is a repository the query looked at and found nothing in. One nothing ran against
    /// is not a miss, because nothing there was looked at.
    /// </summary>
    [Fact]
    public void OnlyARepositoryTheQueryLookedAtCanBePickedAsAMiss()
    {
        var cut = Picking("acme/rule");

        Assert.NotNull(cut.Find("[data-abom-pick-missed='octo/quiet']"));
        Assert.Empty(cut.FindAll("[data-abom-pick-missed='octo/none']"));
        Assert.Empty(cut.FindAll("[data-abom-pick-missed='octo/app']"));

        cut.Find("[data-abom-pick-missed='octo/quiet'] input").Change(true);
        cut.Find("[data-abom-results-ask]").Click();

        var draft = Services.GetRequiredService<RequestDraft>().Request!;
        Assert.Equal("missed", draft.Kind);
        Assert.Equal(("octo/quiet", 0L), (draft.Evidence[0].Repository, draft.Evidence[0].FindingRowId));
    }

    [Fact]
    public void NothingPickedAsksForNothing()
    {
        var cut = Picking("acme/rule");

        Assert.True(cut.Find("[data-abom-results-ask]").HasAttribute("disabled"));
    }

    /// <summary>A request is about one query, so a run of several asks which one first.</summary>
    [Fact]
    public void ARunOfSeveralQueriesAsksWhichOneTheRequestIsAbout()
    {
        var cut = Picking(null);

        Assert.Empty(cut.FindAll("[data-abom-pick-missed]"));
        cut.Find("[data-abom-pick-query='acme/other']").Click();

        Assert.NotNull(cut.Find("[data-abom-pick-missed='octo/quiet']"));
        Assert.Contains("acme/other", cut.Find("[data-abom-results-ask]").TextContent);
    }
}
