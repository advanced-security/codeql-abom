using Abom.WebAssembly.Services;
using Bunit;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// One bar, and it is the whole navigation.
///
/// <para>The drawer spent a fixed slice of every page on four links that never changed, and the
/// two surfaces needing width most — a grid of columns and a graph — are the two that paid for
/// it. A bar costs height once, and height is what this interface has to spare.</para>
///
/// <para>Four surfaces, not nine. A page per question has to invent its own scope, and the
/// questions that are really predicates live as tabs and units on Inventory.</para>
/// </summary>
public class NavMenuTests : AbomTestContext
{
    private IRenderedComponent<Layout.MainLayout> Bar()
    {
        Answer("api/summary", "{}");
        return Render<Layout.MainLayout>();
    }

    private static string[] Hrefs(IRenderedComponent<Layout.MainLayout> bar) =>
        [.. bar.FindAll(".mud-appbar a").Select(a => a.GetAttribute("href") ?? "")];

    /// <summary>
    /// Every surface with an address of its own is reachable from the bar. That is what pays for
    /// removing the drawer: nothing is behind a toggle, so there is nothing to toggle.
    /// </summary>
    [Fact]
    public void TheBarLinksToEverySurfaceAndNothingElse()
    {
        Assert.Equal(["/", "/inventory", "/explore", "/queries", "/settings"], Hrefs(Bar()));
    }

    /// <summary>
    /// Left to right is the journey: what you own, then what matches, then what it touches. The
    /// order is the only thing on screen suggesting where to start.
    /// </summary>
    [Fact]
    public void TheOrderIsTheJourneyRatherThanTheAlphabet()
    {
        var hrefs = Hrefs(Bar());

        Assert.True(Array.IndexOf(hrefs, "/inventory") < Array.IndexOf(hrefs, "/explore"),
            "Inventory names what matches; the Explorer answers what those things touch");
        Assert.Equal("/settings", hrefs[^1]);
    }

    /// <summary>
    /// The product name is the way back to the Dashboard, which is where a reader already clicks.
    /// A second link to the same page would spend bar width saying it twice.
    /// </summary>
    [Fact]
    public void TheProductNameIsTheWayToTheDashboard()
    {
        var bar = Bar();

        var brand = bar.Find("[data-abom-brand]");
        Assert.Equal("/", brand.GetAttribute("href"));
        Assert.Contains("ABOM", brand.TextContent);
        Assert.Single(Hrefs(bar), href => href == "/");
    }

    /// <summary>
    /// Nothing is hidden, so nothing reveals it. A control that opens what is already on screen
    /// does nothing, and it sat in the corner of every page.
    /// </summary>
    [Fact]
    public void ThereIsNothingToToggleOpen()
    {
        var bar = Bar();

        Assert.Empty(bar.FindAll(".mud-drawer"));
        Assert.Empty(bar.FindAll("[aria-label='Toggle navigation']"));
    }

    /// <summary>
    /// Settings is configuration rather than a stop on the journey, so it reads as an icon at the
    /// far end rather than a fourth word competing with the ones that are.
    /// </summary>
    [Fact]
    public void SettingsIsAnIconPastTheThemeControlRatherThanAStopOnTheJourney()
    {
        var bar = Bar();

        var settings = bar.Find("[data-abom-settings]");
        Assert.False(string.IsNullOrWhiteSpace(settings.GetAttribute("aria-label")),
            "an icon carries no words, so the name has to be announced");

        // Read from the rendered order rather than asserted twice: the theme control and the gear
        // are the right-hand end of the bar, and the gear is the outer one.
        var controls = bar.FindAll(".mud-appbar button, .mud-appbar a")
            .Select(e => e.GetAttribute("data-abom-settings") is not null ? "settings"
                       : e.GetAttribute("data-abom-theme") is not null ? "theme" : "other")
            .Where(name => name != "other")
            .ToArray();
        Assert.Equal(["theme", "settings"], controls);
    }

    /// <summary>
    /// The two controls at the right-hand end cannot look like each other. They sit adjacent and
    /// do unrelated things, and the icon that meant "follow the system" was a dial that read as a
    /// second settings gear.
    /// </summary>
    [Theory]
    [InlineData(ThemeChoice.System)]
    [InlineData(ThemeChoice.Light)]
    [InlineData(ThemeChoice.Dark)]
    public void TheThemeControlNeverWearsTheSettingsIcon(ThemeChoice choice)
    {
        Services.GetService<ThemeService>()!.SetAsync(choice, systemPrefersDark: false).Wait();

        var bar = Bar();

        var theme = bar.Find("[data-abom-theme] svg").InnerHtml;
        var settings = bar.Find("[data-abom-settings] svg").InnerHtml;
        Assert.NotEqual(settings, theme);
    }
}
