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

    private const string Refused = """
        {"rowId":4,"predicate":"repositories=octo%2Fapp","mode":"regular",
         "requestedBy":"operator","state":"refused","createdAt":"2026-09-22T16:00:00Z",
         "finishedAt":"2026-09-22T16:00:05Z","results":0,
         "refusal":"installing what abom/custom-queries depends on: Failed to download codeql/actions-all@0.6.2",
         "queries":[{"queryId":"acme/rule","queryVersion":"v1"}],
         "tally":{"pending":1},
         "members":[{"repository":"octo/app","status":"pending","counted":false,"results":0,"byQuery":{}}]}
        """;

    /// <summary>
    /// A run that could not start says why where its results would be, and is not watched as though
    /// it were about to: nothing changes until somebody runs it again.
    /// </summary>
    [Fact]
    public void ARefusedRunSaysWhyNothingRanAndIsNotWatched()
    {
        Answer("api/runs/4", Refused);
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 4L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));

        Assert.Contains("Failed to download codeql/actions-all@0.6.2",
            cut.Find("[data-abom-results-refused]").TextContent);
        Assert.Contains("did not start", cut.Find("[data-abom-results-none]").TextContent);
        Assert.False(cut.Instance.Watching);
    }

    [Fact]
    public void ARunThatStartedCarriesNoRefusal()
    {
        var cut = Render(null);

        Assert.Empty(cut.FindAll("[data-abom-results-refused]"));
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

    /// <summary>A skipped repository carries another run's answer, and says whose.</summary>
    [Fact]
    public void ASkippedRepositorySaysWhichRunAnsweredIt()
    {
        Answer("api/runs/3", Run.Replace(
            "\"repository\":\"octo/quiet\",\"status\":\"succeeded\"",
            "\"repository\":\"octo/quiet\",\"status\":\"skipped\",\"detail\":\"already analyzed with these versions, in run 2\""));
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 3L).Add(c => c.QueryId, "acme/rule"));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));
        cut.Find("[data-abom-results-all] input").Change(true);

        Assert.Contains("skipped — already analyzed with these versions, in run 2", cut.Markup);
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

    /// <summary>
    /// A run recorded before ABOM kept each repository's own evaluation counted results it cannot
    /// list, so the repository says to run it again rather than offering nothing to pick.
    /// </summary>
    [Fact]
    public void ARepositoryCountedBeforeItsEvaluationWasKeptSaysToRunItAgain()
    {
        Answer("api/runs/3/results", """
            {"repository":"octo/app","evaluated":true,"unlinked":true,"truncated":false,"findings":[]}
            """);
        var cut = Render("acme/rule");
        cut.Find("[data-abom-results-pick]").Click();

        cut.Find("[data-abom-member-findings='octo/app']").Click();

        cut.WaitForAssertion(() => Assert.Contains("run it again",
            cut.Find("[data-abom-member-unlinked]").TextContent));
        Assert.Empty(cut.FindAll("[data-abom-pick-finding]"));
    }

    /// <summary>
    /// A repository counted before results were split by query is not in the picker at all, so the
    /// picker says how many were left out and what would let them be picked.
    /// </summary>
    [Fact]
    public void RepositoriesCountedBeforeTheSplitAreSaidToNeedARerunWhenPicking()
    {
        Answer("api/runs/3", """
            {"rowId":3,"predicate":"repositories=octo%2Fapp,octo%2Fold","mode":"regular","requestedBy":"operator",
             "state":"complete","createdAt":"2026-09-22T16:00:00Z","finishedAt":"","results":53,
             "queries":[{"queryId":"acme/rule","queryVersion":"v1"}],"tally":{"succeeded":2},
             "members":[
               {"repository":"octo/app","status":"succeeded","counted":true,"results":2,"byQuery":{"acme/rule":2}},
               {"repository":"octo/old","status":"succeeded","counted":true,"results":51,"byQuery":{}}]}
            """);
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 3L).Add(c => c.QueryId, "acme/rule"));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));

        cut.Find("[data-abom-results-pick]").Click();

        var left = string.Join(" ", cut.Find("[data-abom-results-unpickable]").TextContent
            .Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries));
        Assert.Contains("1 of the 2 repositories this run analyzed was counted before", left);
        Assert.Contains("run it again", left);
        Assert.Empty(cut.FindAll("[data-abom-member-findings='octo/old']"));
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

    private const string SourcedFindings = """
        {"repository":"octo/app","evaluated":true,"truncated":false,"findings":[
          {"findingRowId":41,"ruleId":"acme/rule","filePath":".github/workflows/ci.yml","startLine":6,
           "startColumn":5,"message":"This job runs on a self-hosted runner.","severity":"warning",
           "source":"custom","ruleVersion":"9b5327ab01d7e2f4"},
          {"findingRowId":42,"ruleId":"acme/rule","filePath":".github/workflows/deploy.yml","startLine":9,
           "startColumn":5,"message":"This job runs on a self-hosted runner.","severity":"warning",
           "source":"","ruleVersion":""}]}
        """;

    private const string Trace41 = """
        {"rowId":41,"ruleId":"acme/rule","repository":"octo/app","filePath":".github/workflows/ci.yml",
         "startLine":6,"message":"This job runs on a self-hosted runner.","severity":"warning","provider":"CodeQL",
         "evaluations":[
          {"queryRunId":8544,"invoked":"acme/rule","packs":["abom/custom-queries"],
           "ruleVersion":"9b5327ab01d7e2f4","pack":"abom/custom-queries","packVersion":"0.0.1",
           "packDigest":"af6711d4","source":"custom","cliVersion":"2.27.1","mode":"regular",
           "commit":"0123456789abcdef0123456789abcdef01234567","ranAt":"2026-09-24T10:00:00Z","recordedBy":"run",
           "runs":[{"runId":3,"repository":"octo/app","status":"succeeded"}]},
          {"queryRunId":12,"invoked":"abom-findings.qls",
           "packs":["codeql/actions-queries","advanced-security/abom-actions-findings-src"],
           "ruleVersion":"","pack":"","packVersion":"","packDigest":"","source":"","cliVersion":"2.27.0",
           "mode":"regular","commit":"0123456789abcdef0123456789abcdef01234567","ranAt":"2026-09-20T10:00:00Z",
           "recordedBy":"","runs":[]}]}
        """;

    private IRenderedComponent<Shared.RunResults> Listing()
    {
        Answer("api/runs/3/results", SourcedFindings);
        Answer("api/findings/41", Trace41);
        var cut = Render("acme/rule");
        cut.Find("[data-abom-results-pick]").Click();
        cut.Find("[data-abom-member-findings='octo/app']").Click();
        cut.WaitForAssertion(() => cut.Find("[data-abom-finding-provenance='41']"));
        return cut;
    }

    /// <summary>
    /// Each result names the version and source of the query that reported it. One whose
    /// evaluation kept no record of them says so, rather than borrowing the run's.
    /// </summary>
    [Fact]
    public void EachResultSaysWhichVersionOfWhoseQueryReportedIt()
    {
        var cut = Listing();

        Assert.Equal("custom · version 9b5327ab01d7",
            cut.Find("[data-abom-finding-provenance='41']").TextContent.Trim());
        Assert.Equal("source and version not recorded",
            cut.Find("[data-abom-finding-provenance='42']").TextContent.Trim());
    }

    /// <summary>
    /// A result is traced to every evaluation that reported it: who ran it — the crawl, or a run by
    /// number — in which shape, and the version and pack of the rule's own query. What an older
    /// evaluation did not record is said to be not recorded.
    /// </summary>
    [Fact]
    public void AResultIsTracedToEveryEvaluationThatReportedIt()
    {
        var cut = Listing();
        cut.Find("[data-abom-finding-trace-toggle='41']").Click();

        cut.WaitForAssertion(() => Assert.Equal(2, cut.FindAll("[data-abom-trace-evaluation]").Count));
        var evaluations = cut.FindAll("[data-abom-trace-evaluation]").Select(e => e.TextContent).ToList();
        Assert.Contains("run 3", evaluations[0]);
        Assert.Contains("regular", evaluations[0]);
        Assert.Contains("CodeQL 2.27.1", evaluations[0]);
        Assert.Contains("custom · abom/custom-queries 0.0.1 · version 9b5327ab01d7", evaluations[0]);
        Assert.Equal("/codeql?tab=runs&run=3", cut.Find("[data-abom-trace-run='3']").GetAttribute("href"));
        Assert.Contains("who ran it was not recorded", evaluations[1]);
        Assert.Contains("source and version not recorded", evaluations[1]);
        Assert.Contains("codeql/actions-queries", evaluations[1]);

        cut.Find("[data-abom-finding-trace-toggle='41']").Click();
        Assert.Empty(cut.FindAll("[data-abom-trace-evaluation]"));
    }
}
