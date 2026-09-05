using Microsoft.JSInterop;

namespace Abom.WebAssembly.Services;

/// <summary>
/// Whether views follow a running expansion, and how often they look.
///
/// One setting rather than one per page: Discovery and the Explorer are two views of the same
/// run, and a graph that refreshed on a different beat from the queue beside it would read as
/// two different pictures. Registered as a singleton for the same reason.
/// </summary>
public sealed class FollowService(IJSRuntime js)
{
    private const string StorageKey = "abom.follow";

    /// <summary>
    /// Thirty seconds. Every five was fast enough to re-lay-out the graph while it was still
    /// being read; a minute or more is available for anyone watching a long run.
    /// </summary>
    public const int DefaultSeconds = 30;

    public static readonly IReadOnlyList<int> Choices = [10, 30, 60, 300];

    public bool Enabled { get; private set; }

    public int Seconds { get; private set; } = DefaultSeconds;

    /// <summary>
    /// Whether there is anything to follow. Following a loop that is not running refreshes
    /// nothing, so the control says so rather than ticking away against a static picture.
    /// </summary>
    public bool DiscoveryRunning { get; private set; }

    public event Action? Changed;

    public TimeSpan Interval => TimeSpan.FromSeconds(Seconds);

    /// <summary>Restores the saved choice. Following is never restored on, only the interval.</summary>
    public async Task InitializeAsync()
    {
        var saved = await js.InvokeAsync<string?>("abomTheme.load", StorageKey);
        if (int.TryParse(saved, out var seconds) && Choices.Contains(seconds))
        {
            Seconds = seconds;
        }
    }

    /// <summary>
    /// Records whether discovery is alive. Stopping switches following off, because a ticked box
    /// beside a static picture claims something is happening. Starting does not switch it on: a
    /// run should not begin moving the graph under whoever is reading it.
    /// </summary>
    public void SetDiscoveryRunning(bool running)
    {
        if (DiscoveryRunning == running)
        {
            return;
        }
        DiscoveryRunning = running;
        if (!running)
        {
            Enabled = false;
        }
        Changed?.Invoke();
    }

    public void SetEnabled(bool enabled)
    {
        if (Enabled == enabled || (enabled && !DiscoveryRunning))
        {
            return;
        }
        Enabled = enabled;
        Changed?.Invoke();
    }

    public async Task SetSecondsAsync(int seconds)
    {
        if (!Choices.Contains(seconds) || Seconds == seconds)
        {
            return;
        }
        Seconds = seconds;
        await js.InvokeVoidAsync("abomTheme.save", StorageKey, seconds.ToString());
        Changed?.Invoke();
    }

    /// <summary>How the interval reads in a sentence, rather than as a raw count of seconds.</summary>
    public static string Describe(int seconds) =>
        seconds >= 60 ? $"{seconds / 60} min" : $"{seconds}s";
}
