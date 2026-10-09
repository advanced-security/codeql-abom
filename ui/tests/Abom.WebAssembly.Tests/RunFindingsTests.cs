using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A run that stores findings leads from each repository it covered to the findings it stored
/// there, each one opening where findings are reviewed.
/// </summary>
public class RunFindingsTests : AbomTestContext
{
    private const string Stored = """
        {"rowId":7,"predicate":"repositories=octo%2Fapp,octo%2Fquiet",
         "mode":"regular","requestedBy":"operator","state":"complete","held":false,
         "createdAt":"2026-09-22T16:00:00Z","finishedAt":"2026-09-22T16:04:00Z","results":2,
         "queries":[{"queryId":"acme/rule","queryVersion":"v1"},{"queryId":"acme/other","queryVersion":"v2"}],
         "tally":{"succeeded":2},
         "members":[
           {"repository":"octo/app","status":"succeeded","counted":true,"results":2,"byQuery":{"acme/rule":2,"acme/other":0}},
           {"repository":"octo/quiet","status":"succeeded","counted":true,"results":0,"byQuery":{"acme/rule":0,"acme/other":0}}]}
        """;

    private const string AppFound = """
        {"repository":"octo/app","evaluated":true,"held":false,"truncated":false,"findings":[
          {"findingRowId":900,"heldResultId":0,"ruleId":"acme/rule","filePath":".github/workflows/ci.yml",
           "startLine":6,"startColumn":5,"message":"This job runs on a self-hosted runner.","severity":"warning"},
          {"findingRowId":901,"heldResultId":0,"ruleId":"acme/rule","filePath":".github/workflows/release.yml",
           "startLine":3,"startColumn":5,"message":"This job runs on a self-hosted runner.","severity":"warning"}]}
        """;

    private IRenderedComponent<Shared.RunResults> Opened(string? found = null)
    {
        Answer("api/accounts", """{"authenticated":true,"login":"operator","accounts":[]}""");
        Answer("api/runs/7/results", found ?? AppFound);
        Answer("api/runs/7", Stored);
        var cut = Render<Shared.RunResults>(p => p.Add(c => c.RunId, 7L));
        cut.WaitForAssertion(() => cut.Find("[data-abom-run-summary]"));
        return cut;
    }

    /// <summary>The toggle is offered where there is something to list, and named for it.</summary>
    [Fact]
    public void ARepositoryWithResultsOffersItsFindings()
    {
        var cut = Opened();

        Assert.Equal("Findings", cut.Find("[data-abom-run-findings-toggle='octo/app']").TextContent.Trim());
        Assert.Empty(cut.FindAll("[data-abom-run-findings-toggle='octo/quiet']"));

        cut.Find("[data-abom-run-findings-toggle='octo/app']").Click();

        cut.WaitForAssertion(() =>
            Assert.Equal("Hide findings", cut.Find("[data-abom-run-findings-toggle='octo/app']").TextContent.Trim()));
    }

    /// <summary>Each finding the run stored in a repository is listed under it and opens its review.</summary>
    [Fact]
    public void EachFindingARunStoredOpensItsReview()
    {
        var cut = Opened();

        cut.Find("[data-abom-run-findings-toggle='octo/app']").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains(".github/workflows/ci.yml:6", cut.Find("[data-abom-run-findings='octo/app']").TextContent));
        var link = cut.Find("[data-abom-run-finding='900'] a");
        Assert.Equal("/inventory?unit=findings&finding=900&repositories=octo%2Fapp&rules=acme%2Frule",
            link.GetAttribute("href"));
        Assert.Contains("api/runs/7/results?repository=octo%2Fapp",
            Assert.Single(Sent, r => r.Url.Contains("api/runs/7/results")).Url);
    }

    /// <summary>The repository's findings from the run's queries are reviewed together from the same list.</summary>
    [Fact]
    public void ARepositorysFindingsAreReviewedTogether()
    {
        var cut = Opened();

        cut.Find("[data-abom-run-findings-toggle='octo/app']").Click();

        cut.WaitForAssertion(() => cut.Find("[data-abom-run-findings-review='octo/app']"));
        Assert.Equal("/inventory?unit=findings&repositories=octo%2Fapp&rules=acme%2Fother%2Cacme%2Frule",
            cut.Find("[data-abom-run-findings-review='octo/app']").GetAttribute("href"));
    }

    /// <summary>A count recorded before evaluations were kept per repository cannot be listed, and says so.</summary>
    [Fact]
    public void AnUnlinkedCountSaysWhyItCannotBeListed()
    {
        var cut = Opened("""{"repository":"octo/app","evaluated":true,"held":false,"unlinked":true,"findings":[]}""");

        cut.Find("[data-abom-run-findings-toggle='octo/app']").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains("cannot be listed", cut.Find("[data-abom-run-findings='octo/app']").TextContent));
        Assert.Empty(cut.FindAll("[data-abom-run-finding]"));
    }
}
