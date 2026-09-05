using Microsoft.JSInterop;
using MudBlazor;

namespace Abom.WebAssembly.Services;

/// <summary>How the interface decides between light and dark.</summary>
public enum ThemeChoice
{
    /// <summary>Follow the operating system, and keep following it when it changes.</summary>
    System,
    Light,
    Dark,
}

/// <summary>
/// Holds the appearance choice and the palette derived from it.
///
/// The graph renders to a canvas outside the DOM, so it cannot inherit CSS. Its colors are
/// published here instead and pushed across the interop boundary whenever the theme changes.
/// </summary>
public sealed class ThemeService(IJSRuntime js)
{
    private const string StorageKey = "abom.theme";
    private const string ColorKey = "abom.kindColors";

    private Dictionary<string, string> _kindOverrides = [];

    public ThemeChoice Choice { get; private set; } = ThemeChoice.System;

    /// <summary>Whether dark is actually in effect, after resolving <see cref="Choice"/>.</summary>
    public bool IsDark { get; private set; }

    public event Action? Changed;

    /// <summary>Restores a saved choice, falling back to the system preference.</summary>
    public async Task InitialiseAsync(bool systemPrefersDark)
    {
        var saved = await js.InvokeAsync<string?>("abomTheme.load", StorageKey);
        Choice = saved switch
        {
            "light" => ThemeChoice.Light,
            "dark" => ThemeChoice.Dark,
            _ => ThemeChoice.System,
        };

        // Overrides are keyed by kind and stored per scheme, since a color that reads well on
        // black rarely does on white.
        var colors = await js.InvokeAsync<string?>("abomTheme.load", ColorKey);
        _kindOverrides = ParseOverrides(colors);

        Resolve(systemPrefersDark);
    }

    private static Dictionary<string, string> ParseOverrides(string? raw)
    {
        var map = new Dictionary<string, string>();
        if (string.IsNullOrWhiteSpace(raw))
        {
            return map;
        }
        foreach (var pair in raw.Split(';', StringSplitOptions.RemoveEmptyEntries))
        {
            var parts = pair.Split('=', 2);
            if (parts.Length != 2)
            {
                continue;
            }
            // Keys are scheme-prefixed ("d:repo"), so the kind has to be recovered before it can
            // be validated against the known kinds.
            var colon = parts[0].IndexOf(':');
            var kind = colon >= 0 ? parts[0][(colon + 1)..] : parts[0];
            if (KindNames.ContainsKey(kind))
            {
                map[parts[0]] = parts[1];
            }
        }
        return map;
    }

    /// <summary>Reassigns one kind's color, or clears it when the value is null.</summary>
    public async Task SetKindColorAsync(string kind, string? color)
    {
        var key = $"{(IsDark ? "d" : "l")}:{kind}";
        if (string.IsNullOrWhiteSpace(color))
        {
            _kindOverrides.Remove(key);
        }
        else
        {
            _kindOverrides[key] = color;
        }
        await PersistColorsAsync();
        Changed?.Invoke();
    }

    public async Task ResetKindColorsAsync()
    {
        _kindOverrides.Clear();
        await PersistColorsAsync();
        Changed?.Invoke();
    }

    public bool HasKindOverrides => _kindOverrides.Count > 0;

    private async Task PersistColorsAsync()
    {
        var raw = string.Join(';', _kindOverrides.Select(kv => $"{kv.Key}={kv.Value}"));
        await js.InvokeVoidAsync("abomTheme.save", ColorKey, raw);
    }

    private IReadOnlyDictionary<string, string> KindColors(Dictionary<string, string> defaults)
    {
        if (_kindOverrides.Count == 0)
        {
            return defaults;
        }
        var merged = new Dictionary<string, string>(defaults);
        var prefix = IsDark ? "d:" : "l:";
        foreach (var (key, color) in _kindOverrides)
        {
            if (key.StartsWith(prefix, StringComparison.Ordinal))
            {
                merged[key[prefix.Length..]] = color;
            }
        }
        return merged;
    }

    /// <summary>Applies a new system preference, which only matters while following the system.</summary>
    public void SystemPreferenceChanged(bool systemPrefersDark)
    {
        if (Choice == ThemeChoice.System)
        {
            Resolve(systemPrefersDark);
        }
    }

    public async Task SetAsync(ThemeChoice choice, bool systemPrefersDark)
    {
        Choice = choice;
        await js.InvokeVoidAsync("abomTheme.save", StorageKey, choice switch
        {
            ThemeChoice.Light => "light",
            ThemeChoice.Dark => "dark",
            _ => "system",
        });
        Resolve(systemPrefersDark);
    }

    private void Resolve(bool systemPrefersDark)
    {
        IsDark = Choice switch
        {
            ThemeChoice.Light => false,
            ThemeChoice.Dark => true,
            _ => systemPrefersDark,
        };
        Changed?.Invoke();
    }

    /// <summary>
    /// Severity colors, chosen to stay distinguishable on both backgrounds. Severity is never
    /// encoded by color alone in this interface; the label always travels with it.
    /// </summary>
    public string SeverityColor(string severity) => (severity.ToLowerInvariant(), IsDark) switch
    {
        ("critical", true) => "#FF6B6B",
        ("critical", false) => "#C62828",
        ("high", true) => "#FFA94D",
        ("high", false) => "#E65100",
        ("medium", true) or ("moderate", true) => "#FFD43B",
        ("medium", false) or ("moderate", false) => "#F9A825",
        ("low", true) => "#74C0FC",
        ("low", false) => "#1565C0",
        (_, true) => "#909296",
        _ => "#616161",
    };

    /// <summary>The palette the canvas graph draws with, mirroring the CSS theme.</summary>
    public GraphPalette Palette => IsDark
        ? new GraphPalette(
            Background: "#1A1B1E",
            Focus: "#FFD43B",
            Seed: "#4DABF7",
            Upstream: "#9775FA",
            External: "#63E6BE",
            Edge: "rgba(190,195,205,0.35)",
            Label: "#E9ECEF",
            Muted: "#5C5F66",
            Kinds: KindColors(DarkKinds),
            Discovery: DarkDiscovery,
            EdgeSameRepo: "rgba(91,200,255,0.85)",
            EdgeSameOwner: "rgba(255,200,72,0.85)",
            EdgeCrossOwner: "rgba(255,110,156,0.85)")
        : new GraphPalette(
            Background: "#FFFFFF",
            Focus: "#B35C00",
            Seed: "#1971C2",
            Upstream: "#6741D9",
            External: "#0CA678",
            Edge: "rgba(60,65,75,0.28)",
            Label: "#212529",
            Muted: "#ADB5BD",
            Kinds: KindColors(LightKinds),
            Discovery: LightDiscovery,
            EdgeSameRepo: "rgba(11,127,199,0.8)",
            EdgeSameOwner: "rgba(178,106,0,0.8)",
            EdgeCrossOwner: "rgba(194,37,92,0.8)");

    // Kind is the first thing a reader wants to tell apart, so it is what color encodes.
    private static readonly Dictionary<string, string> DarkKinds = new()
    {
        ["org"] = "#FFD43B",
        ["repo"] = "#4DABF7",
        ["workflow"] = "#63E6BE",
        ["reusable_workflow"] = "#38D9A9",
        ["composite_action"] = "#FFA94D",
        ["action"] = "#FF6B6B",
        ["docker_image"] = "#E599F7",
        ["job"] = "#A9B1BA",
        ["step"] = "#868E96",
    };

    private static readonly Dictionary<string, string> LightKinds = new()
    {
        ["org"] = "#B35C00",
        ["repo"] = "#1971C2",
        ["workflow"] = "#0CA678",
        ["reusable_workflow"] = "#087F5B",
        ["composite_action"] = "#E8590C",
        ["action"] = "#C92A2A",
        ["docker_image"] = "#9C36B5",
        ["job"] = "#6C757D",
        ["step"] = "#868E96",
    };

    // The semantic colors, stated once. MudBlazor's components read them from the theme and the
    // graph canvas cannot read CSS at all, so without a single source here the same state ends up
    // two different colors depending on which half of the interface drew it.
    private const string SuccessDark = "#51CF66";
    private const string InfoDark = "#4DABF7";
    private const string WarningDark = "#FFA94D";
    private const string ErrorDark = "#FF6B6B";

    private const string SuccessLight = "#2F9E44";
    private const string InfoLight = "#1971C2";
    private const string WarningLight = "#E8590C";
    private const string ErrorLight = "#C92A2A";

    // Discovery state, for the views that are about coverage rather than about structure. Drawn
    // from the semantic palette, not from the kind palette: a reader already knows green means
    // settled and amber means attention, and reusing a kind's hue would mean two things at once.
    //
    // Queued was the muted colour on the reasoning that "not looked at yet" is what grey says.
    // That was wrong twice over: it was byte-identical to the colour reserved for a node that was
    // never a dependency, and at 2.7:1 on dark and 2.1:1 on light it fell under the 3:1 floor for
    // a graphical object, so a queue of 422 items read as an unfilled or disabled control. It is
    // now a slate that is plainly a colour without competing with the states a reader acts on.
    // There is still no colour for a node that was never queued, because that is a fact rather
    // than a stage.
    private static readonly Dictionary<string, string> DarkDiscovery = new()
    {
        ["queued"] = "#8A929E",
        ["retrying"] = WarningDark,
        ["running"] = InfoDark,
        ["done"] = SuccessDark,
        ["unavailable"] = ErrorDark,
    };

    private static readonly Dictionary<string, string> LightDiscovery = new()
    {
        ["queued"] = "#5F6B7A",
        ["retrying"] = WarningLight,
        ["running"] = InfoLight,
        ["done"] = SuccessLight,
        ["unavailable"] = ErrorLight,
    };

    /// <summary>Human-readable names for the discovery states, used by the frontier legend.</summary>
    public static readonly Dictionary<string, string> DiscoveryNames = new()
    {
        ["queued"] = "Queued",
        ["retrying"] = "Retrying",
        ["running"] = "Discovering",
        ["done"] = "Analyzed",
        ["unavailable"] = "Unavailable",
    };

    /// <summary>Human-readable names for the node kinds, used by the legend and filters.</summary>
    public static readonly Dictionary<string, string> KindNames = new()    {
        ["org"] = "Owner",
        ["repo"] = "Repository",
        ["workflow"] = "Workflow",
        ["reusable_workflow"] = "Reusable workflow",
        ["composite_action"] = "Composite action",
        ["action"] = "Action",
        ["docker_image"] = "Docker image",
        ["job"] = "Job",
        ["step"] = "Step",
    };

    public MudTheme Theme { get; } = new()
    {
        PaletteLight = new PaletteLight
        {
            Primary = "#1971C2",
            Success = SuccessLight,
            Info = InfoLight,
            Warning = WarningLight,
            Error = ErrorLight,
            AppbarBackground = "#F1F3F5",
            AppbarText = "#212529",
            Background = "#FFFFFF",
            DrawerBackground = "#F8F9FA",
        },
        PaletteDark = new PaletteDark
        {
            Primary = "#4DABF7",
            Success = SuccessDark,
            Info = InfoDark,
            Warning = WarningDark,
            Error = ErrorDark,
            AppbarBackground = "#212529",
            AppbarText = "#E9ECEF",
            Background = "#1A1B1E",
            Surface = "#25262B",
            DrawerBackground = "#212529",
        },
        // Stated rather than inherited. With no typography of its own the interface took
        // MudBlazor's defaults, which are sized for prose, and every dense page then talked its
        // own way back down to something readable.
        Typography = new Typography
        {
            Default = new DefaultTypography
            {
                FontFamily = Sans,
                FontSize = "0.875rem",
                LineHeight = "1.45",
            },
            H5 = new H5Typography { FontFamily = Sans, FontSize = "1.35rem", FontWeight = "500" },
            H6 = new H6Typography { FontFamily = Sans, FontSize = "1.05rem", FontWeight = "500" },
            Subtitle1 = new Subtitle1Typography { FontFamily = Sans, FontSize = "0.95rem", FontWeight = "500" },
            Subtitle2 = new Subtitle2Typography { FontFamily = Sans, FontSize = "0.85rem", FontWeight = "600" },
            Body1 = new Body1Typography { FontFamily = Sans, FontSize = "0.875rem", LineHeight = "1.45" },
            Body2 = new Body2Typography { FontFamily = Sans, FontSize = "0.8rem", LineHeight = "1.45" },
            Caption = new CaptionTypography { FontFamily = Sans, FontSize = "0.72rem", LineHeight = "1.4" },
            Overline = new OverlineTypography
            {
                FontFamily = Sans,
                FontSize = "0.68rem",
                FontWeight = "600",
                LetterSpacing = "0.08em",
            },
            Button = new ButtonTypography { FontFamily = Sans, FontSize = "0.78rem", FontWeight = "500" },
        },
    };

    private static readonly string[] Sans =
        ["system-ui", "-apple-system", "Segoe UI", "Roboto", "sans-serif"];
}

/// <summary>Colors the canvas graph draws with.</summary>
public sealed record GraphPalette(
    string Background,
    string Focus,
    string Seed,
    string Upstream,
    string External,
    string Edge,
    string Label,
    string Muted,
    IReadOnlyDictionary<string, string> Kinds,
    IReadOnlyDictionary<string, string> Discovery,
    string EdgeSameRepo,
    string EdgeSameOwner,
    string EdgeCrossOwner);
