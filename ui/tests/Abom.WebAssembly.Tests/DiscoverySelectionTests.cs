using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Selection reached the command line long before it reached the page, so the interface offered
/// the one control that re-clones everything and no way to ask for less.
/// </summary>
public class DiscoverySelectionTests : AbomTestContext
{
    private void Serve(DiscoveryStateBuilder state)
    {
        Answer("api/discovery/status", state.ToJson());
        Answer("api/frontier", "[]");
    }

    // Re-analyzing a tier re-clones every repository in it. Selecting what is actually out of
    // date is the difference between re-running a pack bump and re-cloning the corpus, and it
    // existed for `expand-all` while the page could only ask for the whole tier.
    [Fact]
    public void TheInterfaceCanAskForOnlyWhatIsOutOfDate()
    {
        Serve(DiscoveryStateBuilder.Drained().WithTiers(direct: 522, transitive: 691));
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("Re-analyze", cut.Markup));
        Assert.Contains("out of date", cut.Markup);
    }

    // "Re-analyze this one thing I own" is the request the tier buttons cannot express, and the
    // one an operator who has just fixed something actually makes.
    [Fact]
    public void TheInterfaceCanAskForOneRepository()
    {
        Serve(DiscoveryStateBuilder.Drained());
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("Re-analyze", cut.Markup));
        Assert.NotEmpty(cut.FindAll(".abom-requeue-repo input"));
    }

    // A name is what the request is made of. Sending an empty one asks the server to match
    // nothing and reports success for it.
    [Fact]
    public void RequeueingOneRepositoryWaitsForAName()
    {
        Serve(DiscoveryStateBuilder.Drained());
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("abom-requeue-repo", cut.Markup));

        var button = cut.Find(".abom-requeue-repo button");
        Assert.True(button.HasAttribute("disabled"));
    }

    // A count with nothing to judge it by is an assertion. The identity the selection ran
    // against is what makes it checkable, and it is the one thing the reader cannot derive.
    [Fact]
    public void AStaleSelectionSaysWhatItJudgedAgainst()
    {
        Serve(DiscoveryStateBuilder.Drained());
        Answer("api/discovery/requeue-stale", """
            {"requeued":1742,"expedited":0,"cliVersion":"2.26.4","packs":4,
             "state":{"available":true,"status":{"state":"idle"},"frontier":{},"tiers":{}}}
            """);
        var cut = Render<Pages.Discovery>();
        cut.WaitForAssertion(() => Assert.Contains("out of date", cut.Markup));

        cut.Find(".abom-requeue-stale").Click();

        cut.WaitForAssertion(() => Assert.Contains("1,742", cut.Markup));
        Assert.Contains("2.26.4", cut.Markup);
        Assert.Contains("4 packs", cut.Markup);
    }

    // A corpus that is entirely current is the normal outcome after a crawl. Saying nothing
    // leaves a button that appears not to work.
    [Fact]
    public void ASelectionThatFoundNothingSaysSo()
    {
        Serve(DiscoveryStateBuilder.Drained());
        Answer("api/discovery/requeue-stale", """
            {"requeued":0,"expedited":0,"cliVersion":"2.26.4","packs":4,
             "state":{"available":true,"status":{"state":"idle"},"frontier":{},"tiers":{}}}
            """);
        var cut = Render<Pages.Discovery>();
        cut.WaitForAssertion(() => Assert.Contains("out of date", cut.Markup));

        cut.Find(".abom-requeue-stale").Click();

        cut.WaitForAssertion(() =>
            Assert.Contains("Nothing is out of date", cut.Markup));
    }

    // Processed excludes skips, so a re-analysis of work already on record leaves Analyzed at
    // zero. Reporting only that reads as a loop that has stopped doing anything.
    [Fact]
    public void ACycleThatOnlySkippedIsNotReportedAsIdle()
    {
        Serve(DiscoveryStateBuilder.Running().WithProgress(analyzed: 0, skipped: 40));
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("40 already on record", cut.Markup));
    }

    // Nothing skipped is the ordinary case for a first crawl, and a standing "0 already on
    // record" is noise that makes the number that matters harder to see.
    [Fact]
    public void ARunThatHasSkippedNothingSaysNothingAboutSkipping()
    {
        Serve(DiscoveryStateBuilder.Running().WithProgress(analyzed: 12, skipped: 0));
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("Analyzing", cut.Markup));
        Assert.DoesNotContain("already on record without", cut.Markup);
    }
}
