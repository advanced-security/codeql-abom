using Abom.WebAssembly.Services;
using Bunit;
using Microsoft.Extensions.DependencyInjection;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Every test here is a regression this interface actually shipped. `make ui` compiles Razor and
/// asserts nothing, so all of them reached a browser before anyone noticed.
/// </summary>
public class DiscoveryPageTests : AbomTestContext
{
    private void Serve(DiscoveryStateBuilder state, string frontier = "[]")
    {
        Answer("api/discovery/status", state.ToJson());
        Answer("api/frontier", frontier);
    }

    // Shipped: switching filters left the previous filter's emptiness on screen, so the view
    // answered a question it had not yet asked. On a page whose whole job is separating "none"
    // from "not looked yet", stating either one before the data lands is the worst answer
    // available. The window only exists while the request is in flight, so the stub holds it.
    [Fact]
    public void AFilterStatesNothingUntilItsAnswerHasArrived()
    {
        Serve(DiscoveryStateBuilder.Drained());
        var cut = Render<Pages.Discovery>();
        cut.WaitForAssertion(() => Assert.Contains("Nothing is waiting", cut.Markup));

        var release = Withhold("api/frontier");
        cut.FindAll(".mud-chip")
            .First(c => c.TextContent.Contains("Retrying", StringComparison.Ordinal))
            .Click();

        // Mid-flight: a progress bar, and no claim of any kind about what is in the queue.
        cut.WaitForAssertion(() => Assert.Contains("mud-progress-linear", cut.Markup));
        Assert.DoesNotContain("Nothing is waiting", cut.Markup);
        Assert.DoesNotContain("No dependency has failed", cut.Markup);

        release();
        cut.WaitForAssertion(() => Assert.Contains("No dependency has failed", cut.Markup));
    }

    // Shipped: the filters were ordered to mirror the bar above them, which meant a reader
    // looking for one had to learn the ordering first.
    [Fact]
    public void FiltersReadAllFirstThenAlphabetically()
    {
        Serve(DiscoveryStateBuilder.Drained().WithBuildTime(421).WithAdvisories(22));
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("Analyzed", cut.Markup));

        var labels = cut.FindAll(".mud-chip").Select(c => c.TextContent.Trim()).ToList();
        Assert.StartsWith("All", labels[0]);

        var rest = labels.Skip(1).Select(l => l.Split(' ')[0]).ToList();
        var sorted = rest.OrderBy(l => l, StringComparer.Ordinal).ToList();
        Assert.Equal(sorted, rest);
    }

    // Shipped: a drained queue said every dependency had been analyzed while hundreds in the
    // graph had never been looked inside. Silence about a gap reads as the absence of one.
    [Fact]
    public void ADrainedQueueReportsWhatIsStillUnanalyzed()
    {
        Serve(DiscoveryStateBuilder.Drained().WithUpstreamOnly(415));
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() =>
            Assert.Contains("415 dependencies appear in the graph", cut.Markup));
        Assert.DoesNotContain("Every dependency has been analyzed", cut.Markup);
    }

    // Re-analysis spends minutes of cloning and database building that nothing can take back,
    // so the count goes on the control rather than being discovered afterwards.
    [Fact]
    public void ReAnalysisStatesItsCostBeforeItIsPressed()
    {
        Serve(DiscoveryStateBuilder.Drained().WithTiers(direct: 522, transitive: 691));
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("Re-analyze", cut.Markup));
        Assert.Contains("522", cut.Markup);
        Assert.Contains("691", cut.Markup);
    }

    // A bare clock time is unambiguous only to someone who knows when the page was loaded, and
    // these views are meant to be left open across a long run.
    [Fact]
    public void TimesSayWhichDayTheyBelongTo()
    {
        Serve(DiscoveryStateBuilder.Running()
            .WithReach(pending: 100, threshold: 500, recomputedAt: DateTime.UtcNow.ToString("o")));
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("Usage counts last worked out", cut.Markup));
        Assert.Contains("today at", cut.Markup);
    }

    // The threshold is what makes the pending count mean anything: 100 arrivals is close to a
    // recount or nowhere near it depending on a number the reader cannot otherwise see.
    [Fact]
    public void ThePendingCountIsReportedAgainstTheThresholdThatTriggersARecount()
    {
        Serve(DiscoveryStateBuilder.Running()
            .WithReach(pending: 100, threshold: 500, recomputedAt: DateTime.UtcNow.ToString("o")));
        var cut = Render<Pages.Discovery>();

        cut.WaitForAssertion(() => Assert.Contains("100 of 500", cut.Markup));
    }

    // Shipped: pressing Pause left "Pausing…" and a disabled button on screen long after the
    // loop had finished pausing, because the page only refreshed itself when following was
    // ticked — and following could not be ticked once the loop had stopped. A state the loop is
    // on its way out of is one the page has to watch to the end.
    [Fact]
    public void APauseThatHasFinishedStopsSayingItIsPausing()
    {
        Serve(DiscoveryStateBuilder.Pausing());
        var cut = Render<Pages.Discovery>();
        cut.WaitForAssertion(() => Assert.Contains("Pausing", cut.Markup));

        Serve(DiscoveryStateBuilder.Paused());

        cut.WaitForAssertion(() => Assert.DoesNotContain("Pausing", cut.Markup), Settling);
        Assert.Contains("Start", cut.Markup);
    }

    // Long enough for the page to have watched and answered, short enough that a page which
    // never watches fails rather than hangs.
    private static readonly TimeSpan Settling = TimeSpan.FromSeconds(15);
}
