using Abom.WebAssembly.Services;
using Microsoft.Extensions.DependencyInjection;
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
}
