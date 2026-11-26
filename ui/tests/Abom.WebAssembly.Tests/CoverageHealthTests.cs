using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A failure is retried until it is not, and a reader needs to know which rows have stopped
/// helping: one past the attempt ceiling is stuck rather than on its second try, an unavailable one
/// needs access or names a reference that is gone, and each says when it last failed and when ABOM
/// looks again. A value nothing recorded is a dash, never a date or a cause nobody measured.
/// </summary>
public class CoverageHealthTests : AbomTestContext
{
    private const string Failed = "2026-03-04T05:06:07Z";
    private const string Due = "2026-03-04T09:06:07Z";
    private const string Probed = "2026-03-05T00:00:00Z";
    private static readonly string Next = DateTime.UtcNow.AddHours(4).ToString("yyyy-MM-ddTHH:mm:ssZ");

    public CoverageHealthTests()
    {
        Answer("api/discovery/suites", """{"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}""");
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().ToJson());
        Answer("api/frontier", $$"""
            [{"nodeKey":"octo/stuck","ref":"v1","owner":"octo","repo":"stuck","status":"pending","attempts":8,
              "reason":"exit status 32","nextAttemptAt":"{{Next}}","lastAttemptAt":"{{Failed}}","stuck":true,"tier":"direct"},
             {"nodeKey":"octo/trying","ref":"v1","owner":"octo","repo":"trying","status":"pending","attempts":2,
              "reason":"exit status 32","nextAttemptAt":"","lastAttemptAt":"","stuck":false,"tier":"direct"},
             {"nodeKey":"octo/due","ref":"v1","owner":"octo","repo":"due","status":"pending","attempts":3,
              "reason":"exit status 32","nextAttemptAt":"{{Due}}","lastAttemptAt":"","stuck":false,"tier":"direct"},
             {"nodeKey":"octo/private","ref":"v1","owner":"octo","repo":"private","status":"unavailable","attempts":0,
              "reason":"dependency unavailable: cloning: remote: Repository not found.","cause":"access",
              "lastAttemptAt":"{{Failed}}","probedAt":"{{Probed}}","tier":"direct"},
             {"nodeKey":"octo/empty","ref":"v1","owner":"octo","repo":"empty","status":"unavailable","attempts":0,
              "reason":"dependency unavailable: empty has no workflows or action manifest","cause":"",
              "lastAttemptAt":"","probedAt":"","tier":"transitive"}]
            """);
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/actions", """{"items":[],"total":0}""");
    }

    private IRenderedComponent<Pages.Inventory> Coverage(string state)
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-unit='coverage']")));
        page.Find("[data-abom-unit='coverage']").Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-coverage-version]")));
        page.Find($"[data-abom-coverage-state='{state}']").Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-coverage-reason]")));
        return page;
    }

    private static string Cell(IRenderedComponent<Pages.Inventory> page, string attribute, string key) =>
        page.Find($"[{attribute}='{key}']").TextContent.Trim();

    [Fact]
    public void ARowPastTheAttemptCeilingReadsAsStuckNotRetrying()
    {
        var page = Coverage("retrying");

        Assert.Equal("stuck", Cell(page, "data-abom-coverage-row-state", "octo/stuck@v1"));
        Assert.Equal("retrying", Cell(page, "data-abom-coverage-row-state", "octo/trying@v1"));
    }

    [Fact]
    public void ARetryShowsWhenItLastFailedAndWhenItIsTriedAgain()
    {
        var page = Coverage("retrying");

        Assert.Equal(TimeText.DayAndTime(Failed), Cell(page, "data-abom-coverage-last-attempt", "octo/stuck@v1"));
        Assert.Equal(TimeText.DayAndTime(Next), Cell(page, "data-abom-coverage-next", "octo/stuck@v1"));
        Assert.Equal("—", Cell(page, "data-abom-coverage-last-attempt", "octo/trying@v1"));
        Assert.Equal("—", Cell(page, "data-abom-coverage-next", "octo/trying@v1"));
    }

    // A retry whose time has passed waits only for discovery to run, so it reads as due since then,
    // never as an attempt still to come.
    [Fact]
    public void ARetryWhoseTimeHasPassedReadsAsDue()
    {
        var page = Coverage("retrying");

        Assert.Equal($"due since {TimeText.DayAndTime(Due)}", Cell(page, "data-abom-coverage-next", "octo/due@v1"));
    }

    // What a state or a cause means is on a tip a pointer reaches, not a title that never shows.
    [Fact]
    public void TheStuckStateAndTheCauseSayWhatTheyMean()
    {
        static string? TipOf(AngleSharp.Dom.IElement e) => e.Closest("[data-abom-tip]")?.GetAttribute("data-abom-tip");

        var retrying = Coverage("retrying");
        Assert.Contains("still retries it", TipOf(retrying.Find("[data-abom-coverage-row-state='octo/stuck@v1']")));

        var unavailable = Coverage("unavailable");
        Assert.Contains("Installing the GitHub App", TipOf(unavailable.Find("[data-abom-coverage-cause='octo/private@v1']")));
    }

    [Fact]
    public void AnUnavailableRowSaysWhatWouldHealItAndWhenItWasLastProbed()
    {
        var page = Coverage("unavailable");

        Assert.Equal("needs access", Cell(page, "data-abom-coverage-cause", "octo/private@v1"));
        Assert.Empty(page.FindAll("[data-abom-coverage-cause='octo/empty@v1']"));
        Assert.Equal($"last probed {TimeText.DayAndTime(Probed)}", Cell(page, "data-abom-coverage-next", "octo/private@v1"));
        Assert.Equal("—", Cell(page, "data-abom-coverage-next", "octo/empty@v1"));
        Assert.Equal("—", Cell(page, "data-abom-coverage-last-attempt", "octo/empty@v1"));
    }

    // Finished work has no failure to date, so the whole queue carries neither column.
    [Fact]
    public void TheWholeQueueCarriesNoAttemptColumns()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-unit='coverage']")));
        page.Find("[data-abom-unit='coverage']").Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-coverage-version]")));

        Assert.Empty(page.FindAll("[data-abom-coverage-last-attempt]"));
        Assert.Empty(page.FindAll("[data-abom-coverage-next]"));
    }
}
