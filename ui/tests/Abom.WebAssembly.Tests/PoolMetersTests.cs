using AngleSharp.Dom;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The pools panel shows what each kind of work may spend, what it did spend in its provider's
/// unit, what GitHub reported left, and the freshness its rate buys. An absent measurement must
/// never read as zero, and an estimate must never read as a promise.
/// </summary>
public class PoolMetersTests : AbomTestContext
{
    private const string Pools = """
        {"since":"2026-10-04T11:00:00Z","pools":[
          {"pool":"enumeration","unit":"REST requests","resource":"core",
           "work":"Reading the accounts you declared.","perHour":600,"burst":1,
           "spend":{"pool":"enumeration","unit":"REST requests",
                    "lastHour":{"requests":50,"charged":42},"total":{"requests":90,"charged":80},
                    "since":"2026-10-04T10:00:00Z",
                    "reported":{"remaining":4321,"limit":5000,"resetAt":"2026-10-04T12:30:00Z",
                                "at":"2026-10-04T12:00:00Z"}},
           "estimate":null,
           "noEstimate":"No estimate: what reading an account costs is known only once it has been read."},
          {"pool":"reference probing","unit":"git requests",
           "work":"The recheck sweep: asking whether an analyzed reference has moved.",
           "perHour":600,"burst":1,"spend":null,
           "estimate":{"text":"At 600 git requests an hour, every one of the 725 references that can move would be asked about again within about 1 day. An estimate from the rate, not a promise.",
                       "seconds":86400,"work":725,
                       "checked":"Nothing has measured this yet. The consistency checker's check 10 will compare it with what the lag checks measure; it is not built."}},
          {"pool":"fetching","unit":"git requests",
           "work":"Looking up and cloning the reference a dependency names.",
           "perHour":null,"burst":1,"spend":null,"estimate":null,
           "noEstimate":"No rate is set, so no time follows from one: this work goes as fast as its answers come back."},
          {"pool":"Copilot agents","unit":"Copilot credits",
           "work":"Agents writing queries for you.",
           "unmetered":"Not metered yet: Copilot does not report to this build what a session charged.",
           "perHour":null,"burst":1,"spend":null,"estimate":null}
        ]}
        """;

    private IRenderedComponent<Shared.PoolMeters> RenderPools()
    {
        Answer("api/pools", Pools);
        var cut = Render<Shared.PoolMeters>();
        cut.WaitForElement("[data-abom-pools]");
        return cut;
    }

    private static IElement Row(IRenderedComponent<Shared.PoolMeters> cut, string pool, string unit) =>
        cut.Find($"tr[data-abom-pool='{pool}/{unit}']");

    // A name is not a definition and a label is not a description of what pressing it does.
    [Fact]
    public void EveryHeaderAndActionSaysInASentenceWhatItIs()
    {
        var cut = RenderPools();

        var headers = cut.FindAll("th");
        Assert.NotEmpty(headers);
        foreach (var th in headers)
        {
            var tip = th.QuerySelector("[data-abom-header-tip]")?.GetAttribute("data-abom-header-tip");
            Assert.False(string.IsNullOrWhiteSpace(tip), $"header '{th.TextContent.Trim()}' has no tip");
            Assert.EndsWith(".", tip);
        }
        foreach (var button in cut.FindAll("button"))
        {
            var title = button.GetAttribute("title") ?? button.ParentElement?.GetAttribute("title");
            Assert.False(string.IsNullOrWhiteSpace(title), $"action '{button.TextContent.Trim()}' has no title");
            Assert.EndsWith(".", title);
        }
    }

    // A pool nothing in this build draws on says so, and a rate there would limit nothing.
    /// <summary>
    /// A screen reader announced every pool's two inputs as "none" and as nothing at all. Each says
    /// which column it sets, for which pool, in which unit: two pools share a name and differ by unit.
    /// </summary>
    [Fact]
    public void EveryRateAndBurstSaysWhichPoolItSets()
    {
        var cut = RenderPools();

        var row = Row(cut, "fetching", "git requests");
        Assert.Equal("Rate an hour for fetching, in git requests",
            row.QuerySelector("[data-abom-pool-rate] input")!.GetAttribute("aria-label"));
        Assert.Equal("Burst for fetching, in git requests",
            row.QuerySelector("[data-abom-pool-burst] input")!.GetAttribute("aria-label"));
    }

    [Fact]
    public void APoolNothingDrawsOnSaysSoAndOffersNoRateToSet()
    {
        var cut = RenderPools();

        var row = Row(cut, "Copilot agents", "Copilot credits");
        Assert.Contains("Not metered yet", row.TextContent);
        Assert.Empty(row.QuerySelectorAll("input"));
        var save = row.QuerySelector("button");
        Assert.NotNull(save);
        Assert.True(save!.HasAttribute("disabled"));
        Assert.Contains("would limit nothing", save.ParentElement!.GetAttribute("title"));
    }

    // A pool with no meter has no measurement, which is not a measurement of zero. Nor is it
    // "never": spend older than the history the store keeps is pruned away.
    [Fact]
    public void APoolThatNeverSpentSaysNothingIsRecordedRatherThanZero()
    {
        var cut = RenderPools();

        var spent = Row(cut, "fetching", "git requests").QuerySelector("[data-abom-pool-spent]")!;
        Assert.Contains("None recorded", spent.TextContent);
        Assert.DoesNotContain("0", spent.TextContent);
        Assert.DoesNotContain("never", spent.TextContent);
    }

    // Spend is shown in the provider's unit against the rate, with GitHub's last reported figure.
    [Fact]
    public void AMeterShowsSpendAgainstTheRateAndWhatGitHubLastReported()
    {
        var cut = RenderPools();

        var row = Row(cut, "enumeration", "REST requests");
        Assert.Contains("42 of 600", row.QuerySelector("[data-abom-pool-spent]")!.TextContent);
        Assert.Contains("50 requests", row.QuerySelector("[data-abom-pool-spent]")!.TextContent);
        Assert.Contains("4,321 of 5,000", row.QuerySelector("[data-abom-pool-reported]")!.TextContent);
    }

    // A time shown as a commitment is the coverage lie in another unit, so the estimate says what
    // it follows from and that nothing has measured it.
    [Fact]
    public void AFreshnessEstimateIsWordedAsOneAndNamesWhatWillCheckIt()
    {
        var cut = RenderPools();

        var freshness = Row(cut, "reference probing", "git requests").QuerySelector("[data-abom-pool-estimate]")!;
        Assert.Contains("An estimate from the rate, not a promise", freshness.TextContent);
        Assert.Contains("Nothing has measured this yet", freshness.TextContent);
        Assert.Contains("No rate is set",
            Row(cut, "fetching", "git requests").QuerySelector("[data-abom-pool-estimate]")!.TextContent);
    }

    // A figure belongs in the sentence or column it qualifies, never in a box of its own.
    [Fact]
    public void ThePanelDrawsNoCountTiles()
    {
        var cut = RenderPools();

        Assert.Empty(cut.FindAll("h1, h2, h3, h4, h5, h6"));
        Assert.Empty(cut.FindAll("[class*='mud-typography-h']"));
        Assert.Empty(cut.FindAll(".mud-card, .mud-paper"));
    }

    // What is saved is what was typed, as a guarded write the server can refuse.
    [Fact]
    public void SavingARateSendsItAndAnEmptyRateClearsIt()
    {
        var cut = RenderPools();

        var fetching = Row(cut, "fetching", "git requests");
        fetching.QuerySelector("[data-abom-pool-rate] input")!.Change("120");
        cut.WaitForAssertion(() => Row(cut, "fetching", "git requests").QuerySelector("button")!.Click());
        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Method == "PUT" && r.Url.EndsWith("api/pools")
            && r.Body.Contains("\"pool\":\"fetching\"") && r.Body.Contains("\"perHour\":120")));

        var probing = Row(cut, "reference probing", "git requests");
        probing.QuerySelector("[data-abom-pool-rate] input")!.Change("");
        cut.WaitForAssertion(() => Row(cut, "reference probing", "git requests").QuerySelector("button")!.Click());
        cut.WaitForAssertion(() => Assert.Contains(Sent, r => r.Method == "PUT"
            && r.Body.Contains("\"pool\":\"reference probing\"") && r.Body.Contains("\"perHour\":null")));
    }
}
