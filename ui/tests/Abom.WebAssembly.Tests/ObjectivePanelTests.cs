using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// A crawl whose priorities are invisible is one nobody can steer. These pin what the Discovery
/// page must say about the objective and what pursuing it has cost.
/// </summary>
public class ObjectivePanelTests : AbomTestContext
{
    private const string Working = """
        {"progress":[
           {"depth":1,"total":10,"done":10,"settled":10,"pending":0,"running":0,"available":0},
           {"depth":2,"total":8,"done":3,"settled":3,"pending":5,"running":0,"available":5}],
         "working":2,"hasOutstanding":true,"maxDepth":5,
         "recent":[
           {"cycle":7,"startedAt":"2026-09-07T00:00:00Z","millis":15300,"fetches":3,"builds":3,
            "evaluations":3,"probes":0,"analyzed":3,"skipped":1,"settled":4,"discovered":40,
            "drifted":0,"shallowestIncomplete":2,"hasOutstanding":true}],
         "totals":{"cycles":7,"millis":107100,"fetches":21,"builds":21,"evaluations":21,
                   "probes":0,"analyzed":21,"discovered":280}}
        """;

    private const string Complete = """
        {"progress":[
           {"depth":1,"total":10,"done":10,"settled":10,"pending":0,"running":0,"available":0}],
         "working":0,"hasOutstanding":false,"maxDepth":5,
         "recent":[
           {"cycle":9,"startedAt":"2026-09-07T00:00:00Z","millis":800,"fetches":0,"builds":0,
            "evaluations":0,"probes":50,"analyzed":0,"skipped":0,"settled":0,"discovered":0,
            "drifted":2,"shallowestIncomplete":0,"hasOutstanding":false}],
         "totals":{"cycles":9,"millis":107900,"fetches":21,"builds":21,"evaluations":21,
                   "probes":50,"analyzed":21,"discovered":280}}
        """;

    private void Serve(string objective)
    {
        Answer("api/discovery/objective", objective);
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained().ToJson());
    }

    // "Complete at depth 1, working depth 2" is the claim the whole objective exists to make.
    [Fact]
    public void ThePageNamesTheDepthBeingWorked()
    {
        Serve(Working);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("depth 2", cut.Markup));
    }

    // A complete closure is not "working depth 0". Reporting a depth would be an absent
    // measurement drawn as a measurement.
    [Fact]
    public void ACompleteClosureIsNotReportedAsWorkingDepthZero()
    {
        Serve(Complete);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("Nothing outstanding", cut.Markup));
        Assert.DoesNotContain("depth 0", cut.Markup);
    }

    // The depth cap has to be visible beside completeness, or "complete" reads as "there is
    // nothing more" when it means "nothing more we are allowed to look at".
    [Fact]
    public void TheDepthCapIsShownBesideCompleteness()
    {
        Serve(Complete);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("5", cut.Markup));
        Assert.Contains("as far as", cut.Markup);
    }

    // What a cycle spent is the point: "under budget" is a phrase until it is a number.
    [Fact]
    public void ThePageStatesWhatTheLastCycleSpent()
    {
        Serve(Working);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("15.3", cut.Markup));
        Assert.Contains("40", cut.Markup);
    }

    // Freshness had never run. A cycle that spends probes and no fetches is what that working
    // looks like, and it must be legible as such rather than as a cycle that did nothing.
    [Fact]
    public void ACycleThatOnlyProbedIsNotShownAsIdle()
    {
        Serve(Complete);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("50", cut.Markup));
        Assert.Contains("probe", cut.Markup);
    }

    // A database that has never run a cycle is not one that ran and cost nothing.
    [Fact]
    public void ADatabaseWithNoRecordedCyclesSaysSoRatherThanShowingZeroSpend()
    {
        Serve("""
            {"progress":[],"working":0,"hasOutstanding":false,"maxDepth":5,
             "recent":[],"totals":{"cycles":0,"millis":0,"fetches":0,"builds":0,
                                   "evaluations":0,"probes":0,"analyzed":0,"discovered":0}}
            """);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("No cycle has run yet", cut.Markup));
    }

    // A depth chip counts settled work, which is analyzed plus what could not be fetched. The
    // tier chips count analyzed alone, so on a real corpus the two decompositions of one queue
    // differed by exactly the unavailable count and nothing on screen reconciled them.
    //
    // The fixture makes done and settled differ on purpose: every other fixture here has them
    // equal, which is a shape that cannot exercise this at all.
    private const string WithUnavailable = """
        {"progress":[
           {"depth":1,"total":10,"done":6,"settled":9,"pending":1,"running":0,"available":1}],
         "working":1,"hasOutstanding":true,"maxDepth":5,
         "recent":[],
         "totals":{"cycles":3,"millis":9000,"fetches":6,"builds":6,"evaluations":6,
                   "probes":0,"analyzed":6,"discovered":12}}
        """;

    [Fact]
    public void ADepthCountsSettledWorkAndSaysWhatThatIncludes()
    {
        Serve(WithUnavailable);

        var cut = Render<Shared.DiscoveryControls>();

        // The chip keeps counting settled, because a depth whose remaining rows can never be
        // fetched is finished and must not read as permanently incomplete.
        cut.WaitForAssertion(() => Assert.Contains("9 of 10", Flat(cut.Markup)));
        // And it says so, so the figure can be reconciled with the analyzed count elsewhere.
        Assert.Contains("settled", cut.Markup, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("unavailable", cut.Markup, StringComparison.OrdinalIgnoreCase);
    }

    // The split itself has to be reachable, or "settled includes unavailable" is a rule without
    // the numbers that let a reader apply it.
    [Fact]
    public void ADepthChipCarriesTheAnalyzedAndUnavailableSplit()
    {
        Serve(WithUnavailable);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("9 of 10", Flat(cut.Markup)));
        var chip = cut.Find(".abom-objective-depths [title]");
        var title = chip.GetAttribute("title") ?? "";
        Assert.Contains("6", title);
        Assert.Contains("3", title);
    }

    // Razor line breaks land inside the rendered text, so "9 of 10" is "9 of\n10" in the markup.
    // Normalizing is what keeps the assertion about the figure rather than about the template.
    private static string Flat(string markup) =>
        System.Text.RegularExpressions.Regex.Replace(markup, @"\s+", " ");
}
