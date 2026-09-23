using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The crawl is the only thing in ABOM that runs with nobody watching it, and its state used to be
/// readable only from several folds down inside Settings. What this holds is that the bar answers
/// "is anything happening?" without a navigation, and that it never answers it wrongly.
/// </summary>
public class CrawlStatusTests : AbomTestContext
{
    private const string Working = """
        {"available":true,"resumeOnStart":false,
         "status":{"state":"working","concurrency":4},
         "frontier":{"done":120,"queued":30,"running":4},
         "tiers":{},"version":{}}
        """;

    private const string Paused = """
        {"available":true,"resumeOnStart":false,
         "status":{"state":"paused","concurrency":4},
         "frontier":{"done":120,"queued":30},
         "tiers":{},"version":{}}
        """;

    private const string NeverStarted = """
        {"available":true,"resumeOnStart":false,
         "status":{"state":"stopped","concurrency":4},
         "frontier":{},"tiers":{},"version":{}}
        """;

    private const string Unavailable = """
        {"available":false,"resumeOnStart":false,
         "status":{"state":"stopped"},"frontier":{},"tiers":{},"version":{}}
        """;

    private IRenderedComponent<Layout.MainLayout> Bar(string discovery)
    {
        Answer("api/summary", "{}");
        Answer("api/discovery/status", discovery);
        return Render<Layout.MainLayout>();
    }

    /// <summary>
    /// The bar prints no words of its own — the icon's color carries the state and the composition
    /// bar carries the queue — so the accessible name is the only place the state is in words, and
    /// it is what a screen reader announces.
    /// </summary>
    private static string Name(IRenderedComponent<Layout.MainLayout> bar) =>
        bar.Find("[data-abom-crawl-toggle]").GetAttribute("aria-label") ?? "";

    /// <summary>
    /// The state is on the bar, so it is on every page. Reaching it used to mean opening Settings
    /// and scrolling past the accounts editor, which made "is it running" a navigation.
    /// </summary>
    [Fact]
    public void TheCrawlStateIsReadableFromTheBar()
    {
        var bar = Bar(Working);

        Assert.Equal("working",
            bar.Find("[data-abom-crawl]").GetAttribute("data-abom-crawl-state"));
        Assert.NotNull(bar.Find("[data-abom-crawl-toggle]"));
    }

    /// <summary>
    /// A glyph alone is ambiguous: a pause icon means "press to pause" to one reader and "it is
    /// paused" to another, and those are opposites. The bar shows no words, so the name has to
    /// carry both the state and what pressing would do.
    ///
    /// <para>One render per test because MudBlazor's popover provider claims a section outlet by
    /// a fixed id, so a second <c>MainLayout</c> in one test throws before any assertion runs.</para>
    /// </summary>
    [Fact]
    public void ARunningCrawlSaysSoAndSaysWhatPressingWouldDo()
    {
        var name = Name(Bar(Working));

        Assert.Contains("Crawling", name);
        Assert.Contains("pause", name);
    }

    [Fact]
    public void APausedCrawlSaysSoAndSaysWhatPressingWouldDo()
    {
        var name = Name(Bar(Paused));

        Assert.Contains("Paused", name);
        Assert.Contains("start", name);
    }

    /// <summary>
    /// No word is printed on the bar. It shares the row with the only two words that compete for
    /// attention, and the state is already in the icon's color.
    /// </summary>
    [Fact]
    public void TheBarCarriesNoStateWordOfItsOwn()
    {
        var crawl = Bar(Working).Find("[data-abom-crawl]").TextContent;

        Assert.DoesNotContain("crawling", crawl, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("paused", crawl, StringComparison.OrdinalIgnoreCase);
    }

    /// <summary>
    /// An installation nobody has crawled is not one somebody stopped. Calling the first "paused"
    /// sends a reader looking for the failure that stopped it.
    /// </summary>
    [Fact]
    public void AnInstallationNobodyHasCrawledDoesNotReadAsPaused()
    {
        var name = Name(Bar(NeverStarted));

        Assert.Contains("Not started", name);
        Assert.DoesNotContain("Paused", name);
    }

    /// <summary>
    /// And the reverse, which only a restart produces: a supervisor is new in every process, so a
    /// server that has crawled the whole organization reports its loop as <c>stopped</c>. Reading
    /// that as "not started" tells an operator their corpus does not exist.
    /// </summary>
    [Fact]
    public void ACrawledInstallationReadsAsPausedAfterARestartRatherThanAsNeverStarted()
    {
        var name = Name(Bar("""
            {"available":true,"resumeOnStart":false,"everStarted":true,
             "status":{"state":"stopped","concurrency":4},
             "frontier":{"done":2561,"queued":36},"tiers":{},"version":{}}
            """));

        Assert.Contains("Paused", name);
        Assert.DoesNotContain("Not started", name);
    }

    /// <summary>
    /// What a restart will do is stated where a reader sees the crawl stopped, because that is
    /// where they are deciding whether the arrangement is what they meant.
    /// </summary>
    [Fact]
    public void AStoppedCrawlSaysWhatARestartWillDo()
    {
        Assert.Contains("restart will leave it stopped", Name(Bar(Paused)));
    }

    /// <summary>
    /// The queue's size is a glance, not a navigation, and it is drawn as composition rather than
    /// a percentage: the crawl finds new dependencies as it goes, so a total that grows would make
    /// any "N% complete" run backwards.
    /// </summary>
    [Fact]
    public void TheQueueIsDrawnAsCompositionBesideTheControl()
    {
        var bar = Bar(Working);

        Assert.NotNull(bar.Find("[data-abom-crawl-queue] .abom-queue-bar"));
        Assert.NotEmpty(bar.FindAll("[data-abom-crawl-queue] .abom-queue-seg"));
    }

    /// <summary>
    /// An empty queue has nothing to draw. A full-width bar of one color on an installation that
    /// has never crawled states a measurement nobody took.
    /// </summary>
    [Fact]
    public void AnEmptyQueueDrawsNothingRatherThanAnEmptyBar()
    {
        Assert.Empty(Bar(NeverStarted).FindAll("[data-abom-crawl-queue]"));
    }

    /// <summary>
    /// A crawl that hit a problem and carried on looks identical to one that did not, and that
    /// difference is the whole reason somebody would go and open Settings.
    /// </summary>
    [Fact]
    public void AProblemIsMarkedOnTheBarRatherThanOnlyInSettings()
    {
        var bar = Bar("""
            {"available":true,"resumeOnStart":false,
             "status":{"state":"idle","lastError":"rate limit reached"},
             "frontier":{"done":1},"tiers":{},"version":{}}
            """);

        Assert.NotNull(bar.Find("[data-abom-crawl-problem]"));
    }

    [Fact]
    public void AHealthyCrawlIsNotMarkedAsAProblem()
    {
        Assert.Empty(Bar(Working).FindAll("[data-abom-crawl-problem]"));
    }

    /// <summary>
    /// A server with no discovery has no state to report, and a stopped-looking control there
    /// would invite a reader to press something that cannot work.
    /// </summary>
    [Fact]
    public void AServerWithoutDiscoveryOffersNoCrawlControl()
    {
        Assert.Empty(Bar(Unavailable).FindAll("[data-abom-crawl]"));
    }

    /// <summary>
    /// The refresh interval governs every open view, so it belongs with the thing whose progress
    /// a reader is watching rather than repeated on each page that happens to poll.
    /// </summary>
    [Fact]
    public void TheRefreshIntervalIsSetFromTheBar()
    {
        Assert.NotNull(Bar(Working).Find("[data-abom-crawl-refresh]"));
    }
}
