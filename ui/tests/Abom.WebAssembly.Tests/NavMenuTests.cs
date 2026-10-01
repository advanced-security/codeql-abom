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
        Assert.Equal(["/", "/codeql", "/inventory", "/explore", "/settings"], Hrefs(Bar()));
    }

    /// <summary>Left to right: the queries findings come from, what matches, what it touches.</summary>
    [Fact]
    public void TheOrderIsTheJourneyRatherThanTheAlphabet()
    {
        var hrefs = Hrefs(Bar());

        Assert.True(Array.IndexOf(hrefs, "/codeql") < Array.IndexOf(hrefs, "/inventory"),
            "a finding exists only once a CodeQL query has run");
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

    // @primer/octicons 19.38.0, build/svg/mark-github-24.svg: the Invertocat as GitHub redrew it
    // in 2025. MudBlazor's Icons.Custom.Brands.GitHub is the mark that redraw retired.
    private const string Invertocat =
        "M10.226 17.284c-2.965-.36-5.054-2.493-5.054-5.256 0-1.123.404-2.336 1.078-3.144-.292-.741"
        + "-.247-2.314.09-2.965.898-.112 2.111.36 2.83 1.01.853-.269 1.752-.404 2.853-.404 1.1 0 1.999"
        + ".135 2.807.382.696-.629 1.932-1.1 2.83-.988.315.606.36 2.179.067 2.942.72.854 1.101 2 1.101 "
        + "3.167 0 2.763-2.089 4.852-5.098 5.234.763.494 1.28 1.572 1.28 2.807v2.336c0 .674.561 1.056 "
        + "1.235.786 4.066-1.55 7.255-5.615 7.255-10.646C23.5 6.188 18.334 1 11.978 1 5.62 1 .5 6.188.5 "
        + "12.545c0 4.986 3.167 9.12 7.435 10.669.606.225 1.19-.18 1.19-.786V20.63a2.9 2.9 0 0 1-1.078"
        + ".224c-1.483 0-2.359-.808-2.987-2.313-.247-.607-.517-.966-1.034-1.033-.27-.023-.359-.135-.359"
        + "-.27 0-.27.45-.471.898-.471.652 0 1.213.404 1.797 1.235.45.651.921.943 1.483.943.561 0 .92-.202"
        + " 1.437-.719.382-.381.674-.718.944-.943";

    /// <summary>
    /// GitHub's mark leads the bar, inside the way back rather than beside it. The Invertocat, the
    /// name and what the name stands for are one press target, so whichever a reader reaches for
    /// lands on the Dashboard — and the Dashboard still has one link, not three.
    /// </summary>
    [Fact]
    public void TheInvertocatAndTheNameAreOneWayToTheDashboard()
    {
        var bar = Bar();

        var brand = bar.Find("[data-abom-brand]");
        Assert.Equal("/", brand.GetAttribute("href"));
        Assert.True(brand.FirstElementChild?.HasAttribute("data-abom-mark"),
            "the Invertocat is the first thing in the way back, left of the name");
        Assert.Contains("ABOM", brand.TextContent);
        Assert.Contains("Actions Bill of Materials", brand.TextContent);
        Assert.Single(Hrefs(bar), href => href == "/");
    }

    /// <summary>
    /// The mark is GitHub's, drawn exactly as GitHub publishes it. Its guidelines forbid changing
    /// its shape or proportions, and the substitute closest to hand — MudBlazor's own GitHub icon —
    /// is the drawing GitHub retired.
    /// </summary>
    [Fact]
    public void TheInvertocatIsGitHubsCurrentMarkUnaltered()
    {
        var mark = Bar().Find("[data-abom-mark]");

        Assert.Equal("svg", mark.LocalName);
        Assert.Equal("0 0 24 24", mark.GetAttribute("viewBox"));
        Assert.Equal(mark.GetAttribute("width"), mark.GetAttribute("height"));
        var path = Assert.Single(mark.Children);
        Assert.Equal("path", path.LocalName);
        Assert.Equal(Invertocat, path.GetAttribute("d"));
        Assert.Null(mark.GetAttribute("transform"));
        Assert.Null(path.GetAttribute("transform"));
    }

    /// <summary>
    /// The mark decorates the name rather than naming the link. Announced, it reads as "image"
    /// before "ABOM", or as "GitHub" — which tells a screen reader user the link leaves ABOM.
    /// </summary>
    [Fact]
    public void TheInvertocatIsNotAnnouncedBesideTheNameItDecorates()
    {
        var mark = Bar().Find("[data-abom-brand] [data-abom-mark]");

        Assert.Equal("true", mark.GetAttribute("aria-hidden"));
        Assert.Equal("false", mark.GetAttribute("focusable"));
    }

    /// <summary>
    /// The product's name is not a heading. As an h6 it came before every page's h1, so the first
    /// heading a screen reader met on any page was the lowest rank, naming no page at all.
    /// </summary>
    [Fact]
    public void TheProductNameIsNotAHeading()
    {
        var brand = Bar().Find("[data-abom-brand]");

        Assert.Empty(brand.QuerySelectorAll("h1, h2, h3, h4, h5, h6"));
    }

    /// <summary>
    /// White on the dark bar, black on the light one, and switched with the scheme rather than on
    /// the next navigation. GitHub's guidelines allow the Invertocat in white or black, and the
    /// bar's own text color is a grey in both schemes.
    /// </summary>
    [Fact]
    public async Task TheInvertocatIsWhiteOnTheDarkBarAndBlackOnTheLightOne()
    {
        var theme = Services.GetService<ThemeService>()!;
        var bar = Bar();

        // After the first render, because rendering restores the saved choice.
        await bar.InvokeAsync(() => theme.SetAsync(ThemeChoice.Dark, systemPrefersDark: false));
        bar.WaitForAssertion(() =>
            Assert.Equal("#FFFFFF", bar.Find("[data-abom-mark]").GetAttribute("fill")));

        await bar.InvokeAsync(() => theme.SetAsync(ThemeChoice.Light, systemPrefersDark: true));
        bar.WaitForAssertion(() =>
            Assert.Equal("#000000", bar.Find("[data-abom-mark]").GetAttribute("fill")));
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
    public async Task TheThemeControlNeverWearsTheSettingsIcon(ThemeChoice choice)
    {
        await Services.GetService<ThemeService>()!.SetAsync(choice, systemPrefersDark: false);

        var bar = Bar();

        var theme = bar.Find("[data-abom-theme] svg").InnerHtml;
        var settings = bar.Find("[data-abom-settings] svg").InnerHtml;
        Assert.NotEqual(settings, theme);
    }
}
