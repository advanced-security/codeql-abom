using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A repository Git could not fetch and one that names no repository are both unavailable, and
/// only one of them is fixed by fixing access. The reason recorded for each is what tells them
/// apart, so the views that list failures show it.
/// </summary>
public class CoverageReasonTests : AbomTestContext
{
    private const string Reason = "dependency unavailable: fatal: could not read Username";

    public CoverageReasonTests()
    {
        Answer("api/discovery/suites", """{"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}""");
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().ToJson());
        Answer("api/frontier", $$"""
            [{"nodeKey":"octo/private","ref":"","owner":"octo","repo":"private",
              "status":"unavailable","depth":1,"score":1,"attempts":3,"analysisCount":0,
              "resolvedSha":"","discoveredFrom":"octo/app","reason":"{{Reason}}",
              "claimedAt":"","nextAttemptAt":"","supplyChain":"runtime","analyzedAt":"",
              "tier":"direct"}]
            """);
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/actions", """{"items":[],"total":0}""");
    }

    private IRenderedComponent<Pages.Inventory> Coverage()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-unit='coverage']")));
        page.Find("[data-abom-unit='coverage']").Click();
        page.WaitForAssertion(() => Assert.NotEmpty(page.FindAll("[data-abom-coverage-version]")));
        return page;
    }

    [Fact]
    public void UnavailableWorkSaysWhyItCouldNotBeFetched()
    {
        var page = Coverage();
        page.Find("[data-abom-coverage-state='unavailable']").Click();

        page.WaitForAssertion(() =>
            Assert.Contains(Reason, page.Find("[data-abom-coverage-reason]").TextContent));
    }

    // Finished work has no failure to explain, and a column of dashes beside it is width spent
    // on nothing.
    [Fact]
    public void TheWholeQueueIsListedWithoutAReasonColumn()
    {
        var page = Coverage();

        Assert.Empty(page.FindAll("[data-abom-coverage-reason]"));
    }
}
