using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>How severe findings are, in the bands code scanning uses for a rule's own score.</summary>
///
/// <remarks>
/// The server reads each score into its band, so no threshold is kept here. Unknown is a rule that
/// declares no score, which is a fact about the rule and never the lowest band.
/// </remarks>
public static class SeverityText
{
    private static readonly string[] Bands = ["critical", "high", "medium", "low", "unknown"];

    /// <summary>How many findings sit in each band, the most severe first.</summary>
    public static string Summary(IEnumerable<FindingFacet> facets) =>
        Join(facets.GroupBy(f => f.Band).Select(g => (g.Key, g.Sum(f => f.Count))));

    /// <summary>The same for counts the server already split by band.</summary>
    public static string Summary(SeverityCounts c) =>
        Join([("critical", c.Critical), ("high", c.High), ("medium", c.Medium), ("low", c.Low),
            ("unknown", c.Unknown)]);

    private static string Join(IEnumerable<(string Band, int Count)> counts) =>
        string.Join(" \u00b7 ", counts
            .Where(c => c.Count > 0)
            .OrderBy(c => Array.IndexOf(Bands, c.Band) is var at and >= 0 ? at : Bands.Length)
            .Select(c => $"{c.Count:N0} {c.Band}"));
}
