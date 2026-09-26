using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// What a row was analyzed with, and when. Both were recorded in the store long before anything
/// rendered them, which is why the page could show a tier as empty while it held 522 things.
/// </summary>
public class AnalysisIdentityTests : AbomTestContext
{
    private void Serve(string suites, string frontier = "[]", DiscoveryStateBuilder? state = null)
    {
        Answer("api/discovery/suites", suites);
        Answer("api/discovery/status", (state ?? DiscoveryStateBuilder.Drained()).ToJson());
        Answer("api/frontier", frontier);
        Answer("api/views", """[{"name":"owned","title":"What you own","predicate":"scope=owned","builtin":true}]""");
        Answer("api/actions", """{"items":[],"total":0}""");
    }

    // The queue is a unit of the Inventory, so a test about its rows selects that unit exactly as
    // a reader would.
    private IRenderedComponent<Pages.Inventory> Coverage()
    {
        var page = Render<Pages.Inventory>();
        page.WaitForAssertion(() => Assert.NotNull(page.Find("[data-abom-unit='coverage']")));
        page.Find("[data-abom-unit='coverage']").Click();
        return page;
    }

    // Re-analyzing a tier moves every row out of `done`, so a header counting only analyzed rows
    // announced that a tier holding 522 dependencies held none. Size and analyzed are different
    // questions: binding a suite costs by the first, re-running costs by the second.
    [Fact]
    public void ATierBeingReAnalyzedStillStatesHowManyDependenciesItHolds()
    {
        Serve("""
            {"available":["advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls"],
             "tiers":{"direct":[],"transitive":[]},
             "counts":{"direct":{"total":522,"analyzed":0},"transitive":{"total":691,"analyzed":691}}}
            """,
            state: DiscoveryStateBuilder.Drained().WithTiers(522, 691).WithDirectAnalyzed(0));

        var cut = Render<Shared.TierSuites>();

        cut.WaitForAssertion(() => Assert.Contains("522 dependencies", cut.Markup));
        Assert.Contains("0 analyzed", cut.Markup);
    }

    // Depth is 1 for every row at the top of the ranking, because the dependencies named by the
    // most repositories are the ones named directly. The column read as a constant exactly where
    // it was looked at, so it states the tier instead — which is also what decides the suites.
    [Fact]
    public void ARowNamesItsTierRatherThanItsHopCount()
    {
        Serve("""
            {"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}
            """,
            """
            [{"nodeKey":"actions/checkout","owner":"actions","repo":"checkout","status":"done",
              "depth":1,"score":9,"attempts":0,"analysisCount":1,"resolvedSha":"abcdef123456",
              "discoveredFrom":"octo/app","reason":"","claimedAt":"","nextAttemptAt":"",
              "supplyChain":"runtime","analyzedAt":"2026-01-01T00:00:00Z","tier":"direct"}]
            """);

        var cut = Coverage();

        cut.WaitForAssertion(() => Assert.Contains("actions/checkout", cut.Markup));
        Assert.Contains(">Tier<", cut.Markup);
        Assert.DoesNotContain(">Depth<", cut.Markup);
    }

    // A row analyzed months ago and one analyzed this morning are different evidence, and the
    // decision to re-analyze a tier replaces an age distribution nothing else states.
    [Fact]
    public void ARowSaysWhenItWasLastAnalyzed()
    {
        Serve("""
            {"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}
            """,
            """
            [{"nodeKey":"actions/checkout","owner":"actions","repo":"checkout","status":"done",
              "depth":1,"score":9,"attempts":0,"analysisCount":1,"resolvedSha":"abcdef123456",
              "discoveredFrom":"octo/app","reason":"","claimedAt":"","nextAttemptAt":"",
              "supplyChain":"runtime","analyzedAt":"2026-01-01T00:00:00Z","tier":"direct"}]
            """);

        var cut = Coverage();

        // The queue opens on every state, so an analyzed row is present without narrowing first.
        cut.WaitForAssertion(() => Assert.Contains("Last analyzed", cut.Markup));
    }

    // Nothing has analyzed a queued dependency, so it has no date. Printing one would be the same
    // error as drawing an unmeasured node at the smallest size.
    [Fact]
    public void AQueuedRowStatesNoAnalysisTimeAtAll()
    {
        Serve("""
            {"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}
            """,
            """
            [{"nodeKey":"actions/never","owner":"actions","repo":"never","status":"pending",
              "depth":2,"score":1,"attempts":0,"analysisCount":0,"resolvedSha":"",
              "discoveredFrom":"octo/app","reason":"","claimedAt":"","nextAttemptAt":"",
              "supplyChain":"runtime","analyzedAt":"","tier":"transitive"}]
            """);

        var cut = Coverage();

        cut.WaitForAssertion(() => Assert.Contains("actions/never", cut.Markup));
        Assert.DoesNotContain("Analyzed 1970", cut.Markup);
        Assert.Contains("transitive", cut.Markup);
    }

    // A row analyzed under the graph pack alone has no findings because none were looked for,
    // and today that is drawn identically to a row that was searched and came back clean. Those
    // are opposite statements, so the row has to name what ran.
    [Fact]
    public void AnAnalyzedRowNamesTheSuitesThatProducedIt()
    {
        Serve("""
            {"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}
            """,
            """
            [{"nodeKey":"actions/checkout","owner":"actions","repo":"checkout","status":"done",
              "depth":1,"score":9,"attempts":0,"analysisCount":1,"resolvedSha":"abcdef123456",
              "discoveredFrom":"octo/app","reason":"","claimedAt":"","nextAttemptAt":"",
              "supplyChain":"runtime","analyzedAt":"2026-01-01T00:00:00Z","tier":"direct",
              "suites":["codeql/actions-queries@0.6.34"],"suitesKnown":true,
              "cliVersion":"2.26.4"}]
            """);

        var cut = Coverage();

        cut.WaitForAssertion(() => Assert.Contains("Analyzed with", cut.Markup));
        Assert.Contains("actions-queries", cut.Markup);
    }

    // 1,555 of 1,707 runs on the real corpus predate pack identity, so most analyzed rows can
    // name nothing. Blank would read as "no suites ran", which is the claim this cannot make.
    [Fact]
    public void ARowAnalyzedBeforeSuitesWereRecordedSaysSoRatherThanLookingUnanalyzed()
    {
        Serve("""
            {"available":[],"tiers":{"direct":[],"transitive":[]},"counts":{}}
            """,
            """
            [{"nodeKey":"actions/cache","owner":"actions","repo":"cache","status":"done",
              "depth":1,"score":9,"attempts":0,"analysisCount":1,"resolvedSha":"abcdef123456",
              "discoveredFrom":"octo/app","reason":"","claimedAt":"","nextAttemptAt":"",
              "supplyChain":"runtime","analyzedAt":"2026-01-01T00:00:00Z","tier":"direct",
              "suites":[],"suitesKnown":false,"cliVersion":""}]
            """);

        var cut = Coverage();

        cut.WaitForAssertion(() => Assert.Contains("not recorded", cut.Markup));
    }
}
