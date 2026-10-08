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

    private const string TookFromCodeScanning = """
        {"progress":[
           {"depth":0,"total":3,"done":3,"settled":3,"pending":0,"running":0,"available":0}],
         "working":0,"hasOutstanding":false,"maxDepth":5,
         "recent":[
           {"cycle":4,"startedAt":"2026-10-07T00:00:00Z","millis":9000,"fetches":3,"builds":1,
            "evaluations":3,"probes":0,"analyzed":3,"skipped":0,"settled":3,"discovered":0,
            "drifted":0,"shallowestIncomplete":0,"hasOutstanding":false,
            "codeScanning":{"databases":2,"analyses":1,
                            "refused":{"database: another commit":1,"analysis: no analysis":2}}}],
         "totals":{"cycles":7,"millis":30000,"fetches":9,"builds":5,"evaluations":9,"probes":0,
                   "analyzed":9,"discovered":0,"codeScanningCycles":4,
                   "codeScanning":{"databases":3,"analyses":1,
                                   "refused":{"database: another commit":4,"analysis: no analysis":2}}}}
        """;

    // What code scanning supplied is what saved a build, so the round says it beside the builds it
    // ran, with what was refused grouped by what was asked for.
    [Fact]
    public void TheLastRoundSaysWhatCodeScanningSuppliedAndWhatWasRefused()
    {
        Serve(TookFromCodeScanning);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-code-scanning-round]")));
        var round = Flat(cut.Find("[data-abom-code-scanning-round]").TextContent);
        Assert.Contains("Code scanning supplied 2 databases and 1 analysis.", round);
        Assert.Contains("Databases refused: another commit.", round);
        Assert.Contains("Analyses refused: no analysis ×2.", round);
        Assert.DoesNotContain("database: ", round);
        Assert.Contains("1 database build,", Flat(cut.Markup));
    }

    // Twenty refusals read as one list of keys; grouped by what was refused, largest first, the
    // reason that cost the most builds is the first one read. A key the page has never heard of
    // is still shown as said.
    [Fact]
    public void RefusalsAreGroupedByWhatWasRefusedLargestFirst()
    {
        Serve(TookFromCodeScanning.Replace(
            "\"refused\":{\"database: another commit\":1,\"analysis: no analysis\":2}",
            "\"refused\":{\"analysis: another configuration\":1,\"database: another CodeQL\":2,"
            + "\"database: no database\":16,\"analysis: no analysis\":14,\"something new\":3}"));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-code-scanning-round]")));
        var round = Flat(cut.Find("[data-abom-code-scanning-round]").TextContent);
        Assert.Contains("Databases refused: no database ×16, another CodeQL ×2. "
            + "Analyses refused: no analysis ×14, another configuration. "
            + "Also refused: something new ×3.", round);
    }

    // A round recorded before ABOM kept what code scanning supplied says nothing about it, which is
    // not the same as a round code scanning supplied nothing in.
    [Fact]
    public void ARoundThatDidNotRecordCodeScanningDoesNotClaimItSuppliedNothing()
    {
        Serve(Working);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("15.3", cut.Markup));
        Assert.Empty(cut.FindAll("[data-abom-code-scanning-round]"));
        Assert.DoesNotContain("Code scanning supplied", cut.Markup);
    }

    // The totals count only the rounds that kept what code scanning supplied, and say how many.
    [Fact]
    public void TheTotalsSayWhatCodeScanningSuppliedAcrossTheRoundsThatKeptIt()
    {
        Serve(TookFromCodeScanning);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-code-scanning-totals]")));
        var totals = Flat(cut.Find("[data-abom-code-scanning-totals]").TextContent);
        Assert.Contains("Across the 4 of 7 rounds that kept it, code scanning supplied 3 databases and 1 analysis.",
            totals);
        Assert.Contains("Databases refused: another commit ×4. Analyses refused: no analysis ×2.", totals);
    }

    // A crawl that never asks code scanning takes nothing, which reads as refusal unless the page says why.
    [Fact]
    public void ACrawlThatAsksCodeScanningNothingSaysWhy()
    {
        Answer("api/discovery/objective", Working);
        Answer("api/discovery/status", DiscoveryStateBuilder.Drained()
            .WithCodeScanningUnasked("this server was started with --code-scanning-reuse=false").ToJson());

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-code-scanning-unasked]")));
        var said = Flat(cut.Find("[data-abom-code-scanning-unasked]").TextContent);
        Assert.Contains("Code scanning is not asked", said);
        Assert.Contains("--code-scanning-reuse=false", said);
    }

    // Settled counts what is finished, and nothing to analyze is finished without being analyzed or
    // having failed to fetch: folded into "could not be fetched" it read as access to fix.
    [Fact]
    public void NothingToAnalyzeIsNotCountedAsCouldNotBeFetched()
    {
        Serve("""
            {"progress":[
               {"depth":0,"total":31,"done":22,"settled":29,"nothing":6,"pending":2,"running":0,"available":2}],
             "working":0,"hasOutstanding":true,"maxDepth":5,"recent":[],
             "totals":{"cycles":0,"millis":0,"fetches":0,"builds":0,"evaluations":0,"probes":0,
                       "analyzed":0,"discovered":0}}
            """);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-objective-settled]")));
        var settled = cut.Find("[data-abom-objective-settled]").TextContent;
        Assert.Contains("6 with nothing to analyze", settled);
        Assert.Contains("1 that could not be fetched", settled);
        var chip = cut.Find(".abom-objective-depths .mud-chip").GetAttribute("title") ?? "";
        Assert.Contains("22 analyzed, 6 with nothing to analyze, 1 unavailable", chip);
    }

    private static string Staged(bool outstanding, string stages) => $$$"""
        {"progress":[
           {"depth":1,"total":10,"done":9,"settled":9,"pending":1,"running":0,"available":1,"refresh":1}],
         "working":1,"hasOutstanding":{{{(outstanding ? "true" : "false")}}},"maxDepth":5,
         "stages":{{{stages}}},"recent":[],
         "totals":{"cycles":0,"millis":0,"fetches":0,"builds":0,"evaluations":0,"probes":0,
                   "analyzed":0,"discovered":0}}
        """;

    // Work for a repository nothing owned reaches is never claimed. Counted among the queued without
    // a word, it reads as work the crawl is about to do.
    [Fact]
    public void WorkTheClosureNoLongerReachesIsSaidToBeUnclaimed()
    {
        Serve(Staged(true, """{"next":"current","current":3,"unreached":15}"""));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-objective-unreached]")));
        var unreached = cut.Find("[data-abom-objective-unreached]").TextContent;
        Assert.Contains("15 queued", unreached);
        Assert.Contains("nothing you own reaches are not claimed", unreached);
        Assert.Contains("Pruning", unreached);
    }

    // A moved reference waits for every depth, so a reader is told it is waiting rather than
    // left to wonder why a known change has not been analyzed.
    [Fact]
    public void MovedReferencesAreSaidToWaitBehindWorkNeverAnalyzed()
    {
        Serve(Staged(true, """{"next":"current","current":3,"refresh":4}"""));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-objective-stage]")));
        var stage = cut.Find("[data-abom-objective-stage]").TextContent;
        Assert.Contains("4 that moved since they were analyzed wait", stage);
    }

    // Once nothing is left unanalyzed, the refresh is the work, and the panel says which goes first.
    [Fact]
    public void TheRefreshIsNamedOnceEveryDepthIsExplored()
    {
        Serve(Staged(false, """{"next":"refresh","refresh":4}"""));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-objective-stage]")));
        var stage = cut.Find("[data-abom-objective-stage]").TextContent;
        Assert.Contains("Analyzing again 4 that moved since they were analyzed", stage);
        Assert.Contains("longest since analyzed first", stage);
        var chip = cut.Find(".abom-objective-depths .mud-chip").GetAttribute("title") ?? "";
        Assert.Contains("1 being analyzed again after its reference moved", chip);
    }

    // Asked for by name, or moved by an owned push, is a stage of its own, ahead of the rest.
    [Fact]
    public void WorkAskedForByNameOrPushedIsCounted()
    {
        Serve(Staged(true, """{"next":"expedited","expedited":2,"current":3}"""));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.NotEmpty(cut.FindAll("[data-abom-objective-stage]")));
        Assert.Contains("2 asked for by name, or moved by a push to a repository you own, go first",
            cut.Find("[data-abom-objective-stage]").TextContent);
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

    // A tree read is API budget spent in place of a clone. A round that settled moved references
    // that way says what it paid; one that read none says nothing about trees at all.
    [Fact]
    public void ARoundThatReadTreesSaysWhatItSpentOnThem()
    {
        Serve(Working.Replace("\"probes\":0,\"analyzed\":3", "\"probes\":0,\"treeReads\":6,\"analyzed\":3"));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("6 tree reads", cut.Markup));
    }

    [Fact]
    public void ARoundThatReadNoTreesDoesNotMentionThem()
    {
        Serve(Working);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("15.3", cut.Markup));
        Assert.DoesNotContain("tree read", cut.Markup);
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

        cut.WaitForAssertion(() => Assert.Contains("No round has run yet", cut.Markup));
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

    // Three owned repositories at depth 0 failing every attempt: settled for now, never analyzed.
    private const string WithStuck = """
        {"progress":[
           {"depth":0,"total":28,"done":19,"settled":25,"pending":3,"running":0,"available":3,"stuck":3},
           {"depth":1,"total":10,"done":4,"settled":4,"pending":6,"running":0,"available":6,"stuck":0}],
         "working":1,"hasOutstanding":true,"maxDepth":5,"stuckAfter":8,
         "recent":[],
         "totals":{"cycles":3,"millis":9000,"fetches":6,"builds":6,"evaluations":6,
                   "probes":0,"analyzed":6,"discovered":12}}
        """;

    private const string OnlyStuckLeft = """
        {"progress":[
           {"depth":0,"total":28,"done":19,"settled":25,"pending":3,"running":0,"available":3,"stuck":3},
           {"depth":1,"total":10,"done":10,"settled":10,"pending":0,"running":0,"available":0,"stuck":0}],
         "working":0,"hasOutstanding":false,"maxDepth":5,"stuckAfter":8,
         "recent":[],
         "totals":{"cycles":3,"millis":9000,"fetches":6,"builds":6,"evaluations":6,
                   "probes":0,"analyzed":6,"discovered":12}}
        """;

    // A depth no longer held open by failing rows must still say they were not analyzed.
    [Fact]
    public void ADepthChipSaysHowManyAreStuckAfterRepeatedFailures()
    {
        Serve(WithStuck);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() =>
            Assert.Contains("depth 0: 25 of 28, 3 stuck after repeated failures", Flat(cut.Markup)));
        var chip = cut.FindAll(".abom-objective-depths [title]")[0];
        var title = chip.GetAttribute("title") ?? "";
        Assert.Contains("8 or more", title);
        Assert.Contains("retried", title);
        Assert.DoesNotContain("mud-chip-color-success", chip.OuterHtml);
    }

    // Only stuck rows left is not "every reference reached has been analyzed".
    [Fact]
    public void NothingOutstandingDoesNotClaimStuckWorkWasAnalyzed()
    {
        Serve(OnlyStuckLeft);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("Nothing outstanding", cut.Markup));
        var said = Flat(cut.Find(".abom-objective").TextContent);
        Assert.DoesNotContain("Every reference reached has been analyzed", said);
        Assert.Contains("3 stuck after repeated failures", said);
        Assert.Contains("retried on their own schedule", said);
    }

    // "Nothing deeper is started until it is done" was false whenever the shallow depth waited.
    [Fact]
    public void TheWorkingSentenceSaysDeeperWorkFillsTheRound()
    {
        Serve(Working);

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("depth 2", cut.Markup));
        var said = Flat(cut.Find(".abom-objective").TextContent);
        Assert.DoesNotContain("nothing deeper is started", said);
        Assert.Contains("deeper work fills the rest of each round", said);
    }

    private static string WithUnobserved(int count) =>
        Working.Replace("\"maxDepth\":5,", $"\"maxDepth\":5,\"unobserved\":{count},");

    // A commit analyzed before ABOM kept what each commit declared is a gap in what is known: a
    // route through it can be missing, and a submission reading it is refused. How many are left,
    // and how fast the crawl works through them, is part of what it is working on.
    [Fact]
    public void TheObjectiveSaysHowManyCommitsAreStillToBeObservedAndHowFast()
    {
        Serve(WithUnobserved(1569));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() =>
            Assert.Contains("1,569 commits", Flat(cut.Find("[data-abom-objective-unobserved]").TextContent)));
        var said = Flat(cut.Find("[data-abom-objective-unobserved]").TextContent);
        Assert.Contains("before ABOM kept what each commit declared", said);
        Assert.Contains("refused", said);
        Assert.Contains("20 at a time", said);
    }

    [Fact]
    public void OneCommitIsNotSpokenOfAsSeveral()
    {
        Serve(WithUnobserved(1));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() =>
            Assert.Contains("1 commit was", Flat(cut.Find("[data-abom-objective-unobserved]").TextContent)));
    }

    // The count is taken on every request, so zero is a measurement: every commit a reference
    // resolves to is known, and nothing is shown that would say otherwise.
    [Fact]
    public void NothingIsSaidAboutReobservingOnceEveryCommitIsObserved()
    {
        Serve(WithUnobserved(0));

        var cut = Render<Shared.DiscoveryControls>();

        cut.WaitForAssertion(() => Assert.Contains("depth 2", cut.Markup));
        Assert.Empty(cut.FindAll("[data-abom-objective-unobserved]"));
    }
}
