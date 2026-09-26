namespace Abom.WebAssembly.Services;

/// <summary>
/// Clock times that say which day they belong to.
///
/// A bare "17:44" is unambiguous only to someone who knows the page was loaded today. These
/// views are meant to be left open across a long expansion, which is exactly when that stops
/// being true and the reader has no way to tell.
/// </summary>
public static class TimeText
{
    /// <summary>A local time qualified by the day it fell on.</summary>
    public static string DayAndTime(string timestamp, string format = "HH:mm") =>
        DateTime.TryParse(timestamp, null,
            System.Globalization.DateTimeStyles.AdjustToUniversal, out var at)
            ? DayAndTime(at, format)
            : "an unknown time";

    /// <summary>A local time qualified by the day it fell on.</summary>
    public static string DayAndTime(DateTime utc, string format = "HH:mm")
    {
        var local = utc.Kind == DateTimeKind.Utc ? utc.ToLocalTime() : utc;
        var today = DateTime.Now.Date;
        var day = local.Date == today ? "today"
            : local.Date == today.AddDays(-1) ? "yesterday"
            : local.ToString("d MMM");
        return $"{day} at {local.ToString(format)}";
    }

    /// <summary>A local clock time alone, for rows under a heading that already names the day.</summary>
    public static string Clock(string timestamp) =>
        DateTime.TryParse(timestamp, null,
            System.Globalization.DateTimeStyles.AdjustToUniversal, out var at)
            ? (at.Kind == DateTimeKind.Utc ? at.ToLocalTime() : at).ToString("HH:mm:ss")
            : "--:--:--";

    /// <summary>A rough span, in the minutes a reader plans around rather than seconds.</summary>
    public static string Roughly(long seconds)
    {
        var minutes = (long)Math.Round(seconds / 60.0);
        return seconds < 60 ? "under a minute"
            : minutes < 60 ? $"about {minutes} min"
            : $"about {minutes / 60} h {minutes % 60} min";
    }
}
