using Abom.WebAssembly.Services;
using Microsoft.Extensions.DependencyInjection;
using MudBlazor;
using MudBlazor.Utilities;
using Xunit;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// The palette, checked as numbers rather than judged by eye.
///
/// "Queued" shipped as the same grey as "never a dependency" and at 2.7:1 against the dark
/// background, so on the Discovery page it read as an unfilled or disabled control rather than a
/// state. Colour review by screenshot had passed it repeatedly; arithmetic does not.
///
/// The 3:1 floor is WCAG 2.2 1.4.11 Non-text Contrast, which is the right bar for a legend swatch
/// or a graph node: they carry meaning without being text.
/// </summary>
public class PaletteContrastTests : AbomTestContext
{
    private const double UiComponentFloor = 3.0;
    private const double BodyTextFloor = 4.5;

    private GraphPalette Palette(bool dark)
    {
        var theme = Services.GetRequiredService<ThemeService>();
        theme.SystemPreferenceChanged(dark);
        return theme.Palette;
    }

    /// <summary>The MudBlazor palette, which every chip, alert, button and icon reads from and
    /// which no test measured while the graph's own colours were being checked to two decimals.</summary>
    private Palette Semantic(bool dark)
    {
        var theme = Services.GetRequiredService<ThemeService>().Theme;
        return dark ? theme.PaletteDark : theme.PaletteLight;
    }

    private static string Hex(MudColor color) => color.ToString(MudColorOutputFormats.Hex);

    public static TheoryData<bool> Schemes => new() { true, false };

    /// <summary>WCAG relative luminance. Hex only; the edge colours carry alpha and are judged
    /// against whatever they cross rather than against the background alone.</summary>
    private static double Luminance(string hex)
    {
        var value = hex.TrimStart('#');
        double Channel(int start)
        {
            var v = Convert.ToInt32(value.Substring(start, 2), 16) / 255.0;
            return v <= 0.03928 ? v / 12.92 : Math.Pow((v + 0.055) / 1.055, 2.4);
        }
        return (0.2126 * Channel(0)) + (0.7152 * Channel(2)) + (0.0722 * Channel(4));
    }

    private static double Contrast(string a, string b)
    {
        double la = Luminance(a), lb = Luminance(b);
        return (Math.Max(la, lb) + 0.05) / (Math.Min(la, lb) + 0.05);
    }

    // Every state the queue can be in is drawn and named, or the legend leaves one out and the
    // bar draws it in the colour of something never measured.
    [Theory]
    [MemberData(nameof(Schemes))]
    public void EveryQueueStateHasAColourAndAName(bool dark)
    {
        var p = Palette(dark);
        foreach (var state in new[] { "queued", "retrying", "waiting", "running", "done", "unavailable", "nothing" })
        {
            Assert.True(p.Discovery.ContainsKey(state), $"no {(dark ? "dark" : "light")} colour for {state}");
            Assert.True(ThemeService.DiscoveryNames.ContainsKey(state), $"no legend name for {state}");
        }
        Assert.Equal("Nothing to analyze", ThemeService.DiscoveryNames["nothing"]);
        Assert.Equal("Waiting for code scanning", ThemeService.DiscoveryNames["waiting"]);
    }

    [Theory]
    [MemberData(nameof(Schemes))]
    public void EveryDiscoveryStateIsVisibleAgainstTheCanvas(bool dark)
    {
        var p = Palette(dark);
        foreach (var (state, color) in p.Discovery)
        {
            var ratio = Contrast(color, p.Background);
            Assert.True(ratio >= UiComponentFloor,
                $"{(dark ? "dark" : "light")} discovery '{state}' {color} is {ratio:F2}:1 "
                + $"against {p.Background}, below {UiComponentFloor}:1");
        }
    }

    // A state a reader can act on must not look like the colour reserved for "this was never a
    // dependency". Sharing one grey made a queue of 422 items read as an empty control.
    [Theory]
    [MemberData(nameof(Schemes))]
    public void QueuedIsNotTheColourOfSomethingThatWasNeverMeasured(bool dark)
    {
        var p = Palette(dark);
        Assert.NotEqual(p.Muted.ToUpperInvariant(), p.Discovery["queued"].ToUpperInvariant());
        Assert.True(Contrast(p.Discovery["queued"], p.Muted) >= 1.5,
            $"queued {p.Discovery["queued"]} and muted {p.Muted} are too close to tell apart");
    }

    [Theory]
    [MemberData(nameof(Schemes))]
    public void EveryNodeKindIsVisibleAgainstTheCanvas(bool dark)
    {
        var p = Palette(dark);
        foreach (var (kind, color) in p.Kinds)
        {
            var ratio = Contrast(color, p.Background);
            Assert.True(ratio >= UiComponentFloor,
                $"{(dark ? "dark" : "light")} kind '{kind}' {color} is {ratio:F2}:1 "
                + $"against {p.Background}, below {UiComponentFloor}:1");
        }
    }

    // Labels are read, not merely seen, so they answer to the text bar rather than the UI one.
    [Theory]
    [MemberData(nameof(Schemes))]
    public void GraphLabelsMeetTheTextContrastBar(bool dark)
    {
        var p = Palette(dark);
        var ratio = Contrast(p.Label, p.Background);
        Assert.True(ratio >= BodyTextFloor,
            $"label {p.Label} is {ratio:F2}:1 against {p.Background}");
    }

    // Selection is the one colour that has to win against everything else on screen.
    [Theory]
    [MemberData(nameof(Schemes))]
    public void TheFocusColourStandsOutFromTheCanvas(bool dark)
    {
        var p = Palette(dark);
        var ratio = Contrast(p.Focus, p.Background);
        Assert.True(ratio >= UiComponentFloor,
            $"focus {p.Focus} is {ratio:F2}:1 against {p.Background}");
    }

    /// <summary>HSL saturation. Chroma is what separates a colour from a grey, and nothing the
    /// suite measured before could tell the two apart.</summary>
    private static double Saturation(string hex)
    {
        var value = hex.TrimStart('#');
        double R = Convert.ToInt32(value.Substring(0, 2), 16) / 255.0;
        double G = Convert.ToInt32(value.Substring(2, 2), 16) / 255.0;
        double B = Convert.ToInt32(value.Substring(4, 2), 16) / 255.0;
        double max = Math.Max(R, Math.Max(G, B)), min = Math.Min(R, Math.Min(G, B));
        if (max - min < 1e-9) return 0;
        var l = (max + min) / 2;
        return (max - min) / (1 - Math.Abs((2 * l) - 1));
    }

    /// <summary>Hue in degrees, for measuring how far apart two categories sit on the wheel.</summary>
    private static double Hue(string hex)
    {
        var value = hex.TrimStart('#');
        double R = Convert.ToInt32(value.Substring(0, 2), 16) / 255.0;
        double G = Convert.ToInt32(value.Substring(2, 2), 16) / 255.0;
        double B = Convert.ToInt32(value.Substring(4, 2), 16) / 255.0;
        double max = Math.Max(R, Math.Max(G, B)), min = Math.Min(R, Math.Min(G, B));
        var d = max - min;
        if (d < 1e-9) return 0;
        double h = R >= G && R >= B ? ((G - B) / d) % 6 : G >= B ? ((B - R) / d) + 2 : ((R - G) / d) + 4;
        h *= 60;
        return h < 0 ? h + 360 : h;
    }

    private static double HueDistance(string a, string b)
    {
        var d = Math.Abs(Hue(a) - Hue(b)) % 360;
        return d > 180 ? 360 - d : d;
    }

    // Dark mode is greyscale by construction — every surface, border and disabled control is a
    // grey — so a category drawn in grey reads as chrome rather than as data. "Queued" shipped
    // twice as a grey: first as `Muted` exactly, then as a slate that cleared both the contrast
    // floor and the distance from `Muted` at about 9% saturation. Neither assertion could tell a
    // hue from a grey, which is why this one measures chroma directly.
    private const double CategoryChromaFloor = 0.35;

    [Theory]
    [MemberData(nameof(Schemes))]
    public void EveryDiscoveryStateIsAColourRatherThanAGrey(bool dark)
    {
        var p = Palette(dark);
        foreach (var (state, color) in p.Discovery)
        {
            var s = Saturation(color);
            Assert.True(s >= CategoryChromaFloor,
                $"{(dark ? "dark" : "light")} discovery '{state}' {color} is {s:P0} saturated, "
                + $"below {CategoryChromaFloor:P0} — that is a grey, and dark mode is already grey");
        }
    }

    // Neighbours in one legend have to be tellable apart at a glance, and "queued" beside
    // "retrying" is yellow beside orange, which is the pair most at risk of reading as one.
    //
    // 20 degrees rather than more because three of the five states are warm — unavailable is red,
    // retrying orange, queued yellow — and the warm arc is only about 60 degrees wide. A higher
    // floor is not arithmetically satisfiable without moving a semantic colour off its meaning.
    private const double LegendHueFloor = 20.0;

    [Theory]
    [MemberData(nameof(Schemes))]
    public void DiscoveryStatesAreDistinguishableFromEachOther(bool dark)
    {
        var p = Palette(dark);
        var states = p.Discovery.ToList();
        for (var i = 0; i < states.Count; i++)
        {
            for (var j = i + 1; j < states.Count; j++)
            {
                var distance = HueDistance(states[i].Value, states[j].Value);
                Assert.True(distance >= LegendHueFloor,
                    $"{(dark ? "dark" : "light")} '{states[i].Key}' {states[i].Value} and "
                    + $"'{states[j].Key}' {states[j].Value} are {distance:F0}° apart, "
                    + $"below {LegendHueFloor:F0}° in one legend");
            }
        }
    }

    // MudBlazor draws these as text, not only as fills: an outlined chip's label and border are
    // the semantic colour on the page background, and this interface uses exactly that for
    // "build-time only" and for the pinning levels. So they answer to the text bar.
    //
    // Every colour is reported rather than the first offender, because these are one palette and
    // fixing them one failure at a time hides how much of the scheme is involved: light had two.
    [Theory]
    [MemberData(nameof(Schemes))]
    public void EverySemanticColourIsReadableAsText(bool dark)
    {
        var p = Semantic(dark);
        var background = Hex(p.Background);
        var named = new (string Name, MudColor Color)[]
        {
            ("Primary", p.Primary), ("Success", p.Success), ("Info", p.Info),
            ("Warning", p.Warning), ("Error", p.Error),
        };
        var failures = named
            .Select(n => (n.Name, Hex: Hex(n.Color), Ratio: Contrast(Hex(n.Color), background)))
            .Where(n => n.Ratio < BodyTextFloor)
            .Select(n => $"{n.Name} {n.Hex} is {n.Ratio:F2}:1")
            .ToList();
        Assert.True(failures.Count == 0,
            $"{(dark ? "dark" : "light")} against {background}, below {BodyTextFloor}:1 for text: "
            + string.Join("; ", failures));
    }

    // The appbar states its own background, so it is the one surface where the theme cannot fall
    // back to MudBlazor's pairing of text to background.
    [Theory]
    [MemberData(nameof(Schemes))]
    public void TheAppbarLabelIsReadableOnItsOwnBar(bool dark)
    {
        var p = Semantic(dark);
        var ratio = Contrast(Hex(p.AppbarText), Hex(p.AppbarBackground));
        Assert.True(ratio >= BodyTextFloor,
            $"{(dark ? "dark" : "light")} appbar text {Hex(p.AppbarText)} is {ratio:F2}:1 "
            + $"against {Hex(p.AppbarBackground)}");
    }

    // GitHub's logo guidelines allow the Invertocat in white or black, grey or green only rarely,
    // and ask for the higher-contrast option when in doubt. The bar's text is a grey in both
    // schemes, so the mark cannot simply take the color of the name beside it.
    [Theory]
    [MemberData(nameof(Schemes))]
    public void TheInvertocatIsTheHigherContrastOfBlackAndWhiteOnTheBar(bool dark)
    {
        var theme = Services.GetRequiredService<ThemeService>();
        theme.SystemPreferenceChanged(dark);
        var bar = Hex(Semantic(dark).AppbarBackground);

        var best = Contrast("#FFFFFF", bar) >= Contrast("#000000", bar) ? "#FFFFFF" : "#000000";
        Assert.Equal(best, theme.MarkColor);
    }

    // A scheme can clear every bar above and still say less than the other one. The graph falls
    // back silently when a kind has no colour, so a key in one scheme and not the other is a
    // swatch that goes blank on a theme toggle — and only dark is usually looked at.
    [Fact]
    public void BothSchemesDescribeTheSameSetOfThings()
    {
        var dark = Palette(true);
        var light = Palette(false);
        Assert.Equal(dark.Kinds.Keys.OrderBy(k => k), light.Kinds.Keys.OrderBy(k => k));
        Assert.Equal(dark.Discovery.Keys.OrderBy(k => k), light.Discovery.Keys.OrderBy(k => k));
    }

    // The legend prints a name from KindNames and a swatch from the palette. A name with no
    // colour behind it is a row that reads as an empty checkbox.
    [Theory]
    [MemberData(nameof(Schemes))]
    public void EveryNamedKindHasAColour(bool dark)
    {
        var p = Palette(dark);
        foreach (var kind in ThemeService.KindNames.Keys)
        {
            Assert.True(p.Kinds.ContainsKey(kind),
                $"{(dark ? "dark" : "light")} has no colour for the kind '{kind}', "
                + "which the legend still names");
        }
    }
}
