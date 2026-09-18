using System.Globalization;

namespace Abom.WebAssembly.Services;

/// <summary>
/// A count and the noun it counts, agreeing.
///
/// "1 nodes" is the interface reporting a number it did not read. It happens because a count is
/// interpolated beside a hard-coded plural, which is correct for every value but one and so
/// survives every reading of the markup.
/// </summary>
public static class CountText
{
    /// <summary>A count with its noun.</summary>
    public static string Of(int n, string singular, string plural) =>
        $"{Number(n)} {Noun(n, singular, plural)}";

    /// <summary>The noun alone, for a count typeset in its own element.</summary>
    public static string Noun(int n, string singular, string plural) => n == 1 ? singular : plural;

    /// <summary>The number alone, grouped the way every other figure on every surface is.</summary>
    public static string Number(int n) => n.ToString("N0", CultureInfo.CurrentCulture);
}
