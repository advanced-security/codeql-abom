using Microsoft.JSInterop;

namespace Abom.WebAssembly.Services;

/// <summary>
/// How often an open view refreshes itself.
///
/// One setting rather than one per page: Discovery and the Explorer are two views of the same
/// run, and a graph that refreshed on a different beat from the queue beside it would read as
/// two different pictures. Registered as a singleton for the same reason.
///
/// There is no on/off, because having the page open is the choice to watch it. A checkbox for it
/// was disabled whenever nothing was running, so no view could be asked to watch for something
/// starting, and a page left on "Pausing…" stayed there until someone reloaded it.
/// </summary>
public sealed class FollowService(IJSRuntime js)
{
    private const string IntervalKey = "abom.follow";

    /// <summary>
    /// Thirty seconds. Every five was fast enough to re-lay-out the graph while it was still
    /// being read; a minute or more is available for anyone watching a long run.
    /// </summary>
    public const int DefaultSeconds = 30;

    public static readonly IReadOnlyList<int> Choices = [10, 30, 60, 300];

    public int Seconds { get; private set; } = DefaultSeconds;

    public event Action? Changed;

    public TimeSpan Interval => TimeSpan.FromSeconds(Seconds);

    /// <summary>
    /// Restores the saved interval. Announced rather than applied quietly: views arm their
    /// timers as they render, and the layout restores this afterwards.
    /// </summary>
    public async Task InitializeAsync()
    {
        var saved = await js.InvokeAsync<string?>("abomTheme.load", IntervalKey);
        if (!int.TryParse(saved, out var seconds) || !Choices.Contains(seconds) || seconds == Seconds)
        {
            return;
        }
        Seconds = seconds;
        Changed?.Invoke();
    }

    public async Task SetSecondsAsync(int seconds)
    {
        if (!Choices.Contains(seconds) || Seconds == seconds)
        {
            return;
        }
        Seconds = seconds;
        await js.InvokeVoidAsync("abomTheme.save", IntervalKey, seconds.ToString());
        Changed?.Invoke();
    }

    /// <summary>How the interval reads in a sentence, rather than as a raw count of seconds.</summary>
    public static string Describe(int seconds) =>
        seconds >= 60 ? $"{seconds / 60} min" : $"{seconds}s";
}
