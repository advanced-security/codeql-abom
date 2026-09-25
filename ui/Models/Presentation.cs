using System.Globalization;

namespace Abom.WebAssembly.Models;

/// <summary>
/// How the Explorer drew a scope: which view, grouping, depth and direction, the node in focus, and
/// the three switches that change the picture rather than the question.
/// </summary>
///
/// <remarks>
/// <para>Deliberately not part of <see cref="ViewSpec"/>. A spec is a predicate the server
/// enforces and the pull request gate answers; adding <c>view</c> or <c>groupBy</c> to it would
/// make the server responsible for browser rendering, and would pad every listing's refusal list
/// with terms that are not filters. The server stores this text and never parses it.</para>
///
/// <para>Absent is not a default. A presentation with nothing set means "draw it however you
/// would anyway", which is what a view saved before a knob existed must keep meaning — so a
/// choice left at its default is written as absent rather than spelled out.</para>
/// </remarks>
public sealed record Presentation
{
    public string View { get; init; } = string.Empty;
    public string GroupBy { get; init; } = string.Empty;
    public int Hops { get; init; }
    public string Direction { get; init; } = string.Empty;
    public bool Use3d { get; init; }
    public bool Collapsed { get; init; }
    public bool StableLayout { get; init; }

    /// <summary>
    /// The node drawn in focus, when one is. A key rather than a filter term: naming a node
    /// narrows the drawing to its neighborhood, and no other page draws neighborhoods.
    /// </summary>
    public string Selection { get; init; } = string.Empty;

    /// <summary>Whether anybody chose anything, as opposed to every value happening to be the default.</summary>
    public bool HasAny => ToQueryString().Length > 0;

    public string ToQueryString()
    {
        var parts = new List<string>();
        Add(parts, "view", View);
        Add(parts, "groupBy", GroupBy);
        if (Hops > 0) parts.Add("hops=" + Hops.ToString(CultureInfo.InvariantCulture));
        Add(parts, "direction", Direction);
        if (Use3d) parts.Add("use3d=true");
        if (Collapsed) parts.Add("collapsed=true");
        if (StableLayout) parts.Add("stableLayout=true");
        Add(parts, "selection", Selection);
        return string.Join("&", parts);
    }

    /// <summary>
    /// Reads the drawing choices out of an address, ignoring everything else. The two halves
    /// travel together, and a presentation that swallowed <c>scope</c> would let applying a saved
    /// view silently renarrow the question.
    /// </summary>
    public static Presentation Parse(string? query)
    {
        var found = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var pair in (query ?? string.Empty).TrimStart('?').Split('&'))
        {
            var cut = pair.IndexOf('=', StringComparison.Ordinal);
            if (cut <= 0) continue;
            found[Uri.UnescapeDataString(pair[..cut])] = Uri.UnescapeDataString(pair[(cut + 1)..]);
        }

        return new Presentation
        {
            View = Text(found, "view"),
            GroupBy = Text(found, "groupBy"),
            // A hop count that will not parse is nobody's choice, and zero is a real depth.
            Hops = found.TryGetValue("hops", out var hops)
                   && int.TryParse(hops, NumberStyles.Integer, CultureInfo.InvariantCulture, out var n)
                ? n : 0,
            Direction = Text(found, "direction"),
            Use3d = Flag(found, "use3d"),
            Collapsed = Flag(found, "collapsed"),
            StableLayout = Flag(found, "stableLayout"),
            Selection = Text(found, "selection"),
        };
    }

    private static void Add(List<string> parts, string key, string value)
    {
        if (value.Length > 0) parts.Add($"{key}={Uri.EscapeDataString(value)}");
    }

    private static string Text(Dictionary<string, string> found, string key) =>
        found.TryGetValue(key, out var v) ? v : string.Empty;

    private static bool Flag(Dictionary<string, string> found, string key) =>
        found.TryGetValue(key, out var v) && v == "true";
}
