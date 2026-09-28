using System.Net;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A finished regular run can be set against the code scanning alerts GitHub holds now. The count
/// that matters is what adopting the query would add, and it never travels without the condition
/// it holds under, the merge it counts over, the commit it is exact for, and the repositories it
/// did not compare.
/// </summary>
public class RunComparisonTests : AbomTestContext
{
    private const string Run = """
        {"rowId":7,"predicate":"repositories=octo%2Fapp,octo%2Fold,octo%2Fnever","mode":"regular",
         "requestedBy":"operator","state":"complete","held":true,"cliVersion":"2.27.1",
         "createdAt":"2026-09-22T16:00:00Z","finishedAt":"2026-09-22T16:04:00Z","results":5,
         "queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],
         "tally":{"succeeded":3},
         "members":[
           {"repository":"octo/app","status":"succeeded","counted":true,"results":3,"byQuery":{"actions/unpinned-tag":3}},
           {"repository":"octo/old","status":"succeeded","counted":true,"results":1,"byQuery":{"actions/unpinned-tag":1}},
           {"repository":"octo/never","status":"succeeded","counted":true,"results":1,"byQuery":{"actions/unpinned-tag":1}}]}
        """;

    private const string Compared = """
        {"rowId":3,"runId":7,"requestedBy":"operator","state":"complete","cliVersion":"2.27.1",
         "createdAt":"2026-09-26T10:00:00Z","finishedAt":"2026-09-26T10:00:05Z",
         "queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],
         "condition":"Alerts appear only where Actions analysis is running.",
         "merge":"What appears is the union of what each repository already runs and the pack.",
         "boundary":"Exact for the commit analyzed, and an inference beyond it.",
         "compared":2,"notCompared":1,"pending":0,
         "totals":{"results":4,"open":2,"dismissed":0,"reopens":1,"new":1,"adds":2,"openAlerts":3,"missed":1},
         "rules":[{"ruleId":"actions/unpinned-tag","results":4,"open":2,"dismissed":0,"reopens":1,"new":1,"adds":2,"openAlerts":3,"missed":1}],
         "members":[
           {"repository":"octo/app","analyzedCommit":"c1c1c1c1c1c1c1","status":"compared","counted":true,
            "counts":{"results":3,"open":2,"dismissed":0,"reopens":1,"new":0,"adds":1,"openAlerts":2,"missed":0},
            "rules":[],"alertedCommit":"c1c1c1c1c1c1c1","alertedCli":"2.27.1","headCommit":"c1c1c1c1c1c1c1","staleness":"current","behindBy":null,"exact":true},
           {"repository":"octo/old","analyzedCommit":"a0a0a0a0a0a0a0","status":"compared","counted":true,
            "counts":{"results":1,"open":0,"dismissed":0,"reopens":0,"new":1,"adds":1,"openAlerts":1,"missed":1},
            "rules":[],"alertedCommit":"b9b9b9b9b9b9b9","alertedCli":"2.23.1","headCommit":"f7f7f7f7f7f7f7","staleness":"behind","behindBy":4,"exact":false},
           {"repository":"octo/never","analyzedCommit":"d4d4d4d4d4d4d4","status":"not_compared","reason":"no_code_scanning",
            "detail":"code scanning holds no analysis here (404, no analysis found), so nothing would be raised",
            "counted":true,"counts":{"results":1},"rules":[],"staleness":"","behindBy":null,"exact":false}]}
        """;

    public RunComparisonTests()
    {
        Answer("api/accounts", """{"authenticated":true,"login":"operator","accounts":[]}""");
    }

    private IRenderedComponent<Shared.RunResults> Opened(string comparisons, string run = Run,
        string compared = Compared)
    {
        Answer("api/comparisons/3", compared);
        Answer("api/runs/7/comparisons", comparisons);
        Answer("api/runs/7", run);
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 7L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));
        return cut;
    }

    private static string Words(string text) => System.Text.RegularExpressions.Regex.Replace(text, @"\s+", " ");

    /// <summary>The delta leads, and the condition, the merge and the boundary sit beside it.</summary>
    [Fact]
    public void AComparisonLeadsWithWhatAdoptingWouldAddAndCarriesItsConditions()
    {
        var cut = Opened("""{"comparisons":[{"rowId":3,"runId":7,"state":"complete","members":[]}]}""");

        cut.WaitForAssertion(() => cut.Find("[data-abom-comparison-totals]"));
        var totals = Words(cut.Find("[data-abom-comparison-totals]").TextContent);
        Assert.Contains("would add 2 alerts", totals);
        Assert.Contains("1 new", totals);
        Assert.Contains("1 reopening", totals);
        Assert.Contains("2 already open", totals);
        Assert.Contains("2 of 3 repositories", totals);
        Assert.Contains("Alerts appear only where Actions analysis is running.",
            cut.Find("[data-abom-comparison-condition]").TextContent);
        Assert.Contains("union", cut.Find("[data-abom-comparison-merge]").TextContent);
        Assert.Contains("inference beyond it", cut.Find("[data-abom-comparison-boundary]").TextContent);
        Assert.Contains("1 open alert", Words(cut.Find("[data-abom-comparison-missed]").TextContent));
    }

    /// <summary>A repository nothing compared is named with why, never folded into the count.</summary>
    [Fact]
    public void ARepositoryNotComparedIsNamedWithWhy()
    {
        var cut = Opened("""{"comparisons":[{"rowId":3,"runId":7,"state":"complete","members":[]}]}""");

        cut.WaitForAssertion(() => cut.Find("[data-abom-comparison-not-compared]"));
        var named = Words(cut.Find("[data-abom-comparison-not-compared]").TextContent);
        Assert.Contains("octo/never", named);
        Assert.Contains("no analysis found", named);
        Assert.Empty(cut.FindAll("[data-abom-comparison-member='octo/never']"));
    }

    /// <summary>A repository whose default branch has moved past the commit analyzed says so beside
    /// its own count, and one analyzed at another commit than GitHub's says it is an inference.</summary>
    [Fact]
    public void StalenessIsStatedBesideEachRepositorysCount()
    {
        var cut = Opened("""{"comparisons":[{"rowId":3,"runId":7,"state":"complete","members":[]}]}""");

        cut.WaitForAssertion(() => cut.Find("[data-abom-comparison-member='octo/old']"));
        var old = Words(cut.Find("[data-abom-comparison-member='octo/old']").TextContent);
        Assert.Contains("4 commits", old);
        Assert.Contains("inference", old);
        Assert.Contains("CodeQL 2.23.1", old);
        var app = Words(cut.Find("[data-abom-comparison-member='octo/app']").TextContent);
        Assert.Contains("default branch's head", app);
        Assert.Contains("Measured", app);
    }

    /// <summary>Measured: the same commit under a year-older CodeQL disagreed by 7 results, so the
    /// same commit alone is not a measurement, and the row names both CodeQL versions.</summary>
    [Fact]
    public void TheSameCommitUnderAnotherCodeQLIsNamedAsTheInference()
    {
        var cut = Opened("""{"comparisons":[{"rowId":3,"runId":7,"state":"complete","members":[]}]}""",
            compared: Compared.Replace("\"alertedCommit\":\"b9b9b9b9b9b9b9\"", "\"alertedCommit\":\"a0a0a0a0a0a0a0\""));

        cut.WaitForAssertion(() => cut.Find("[data-abom-comparison-member='octo/old']"));
        var old = Words(cut.Find("[data-abom-comparison-member='octo/old']").TextContent);
        Assert.Contains("same commit under CodeQL 2.23.1", old);
        Assert.Contains("came from CodeQL 2.27.1", old);
    }

    /// <summary>Comparing records who asked.</summary>
    [Fact]
    public void ComparingSendsWhoAsked()
    {
        var cut = Opened("""{"comparisons":[]}""");

        cut.WaitForAssertion(() => cut.Find("[data-abom-comparison-start] button"));
        Assert.Empty(cut.FindAll("[data-abom-comparison-totals]"));
        cut.Find("[data-abom-comparison-start] button").Click();

        cut.WaitForAssertion(() =>
        {
            var sent = Assert.Single(Sent, r => r.Method == "POST" && r.Url.EndsWith("api/runs/7/comparisons"));
            Assert.Contains("\"by\":\"operator\"", sent.Body);
        });
    }

    /// <summary>A comparison the server refuses is shown in its own words, not as an empty result.</summary>
    [Fact]
    public void ARefusedComparisonIsShownInTheServersWords()
    {
        Fail("api/runs/7/comparisons", HttpStatusCode.BadRequest);
        var cut = Opened("""{"comparisons":[]}""");

        cut.WaitForAssertion(() => cut.Find("[data-abom-comparison-start] button"));
        cut.Find("[data-abom-comparison-start] button").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains("refused api/runs/7/comparisons", cut.Find("[data-abom-comparison-refusal]").TextContent));
        Assert.Empty(cut.FindAll("[data-abom-comparison-totals]"));
    }

    /// <summary>Default Setup never builds an extended database, so an extended run offers no
    /// comparison and says why rather than hiding the control.</summary>
    [Fact]
    public void AnExtendedRunOffersNoComparisonAndSaysWhy()
    {
        var cut = Opened("""{"comparisons":[]}""", Run.Replace("\"mode\":\"regular\"", "\"mode\":\"extended\""));

        cut.WaitForAssertion(() => cut.Find("[data-abom-comparison-refused]"));
        Assert.Contains("regular", cut.Find("[data-abom-comparison-refused]").TextContent);
        Assert.Empty(cut.FindAll("[data-abom-comparison-start]"));
    }
}
