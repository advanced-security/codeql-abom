using System.Text.Json;

namespace Abom.WebAssembly.Tests;

/// <summary>
/// Builds the discovery states worth rendering, as the server would publish them.
///
/// JSON rather than typed objects, deliberately: it is what the server actually sends, so a
/// field renamed on one side breaks a test rather than quietly deserializing to a default.
/// </summary>
public sealed class DiscoveryStateBuilder
{
    private string _state = "stopped";
    private int _done, _queued, _running, _unavailable, _buildTime, _advisories, _upstreamOnly;
    private int _reachPending;
    private int _reachThreshold = 500;
    private string _reachRecomputedAt = "";
    private int _direct, _transitive;

    // Defaults to "all of it analyzed", which is the steady state. A requeue is the case where the
    // two diverge, so it is set explicitly.
    private int? _directAnalyzed;

    public static DiscoveryStateBuilder NeverStarted() => new();

    public static DiscoveryStateBuilder Running() =>
        new DiscoveryStateBuilder().With(state: "working", done: 500, queued: 120, running: 20);

    public static DiscoveryStateBuilder Drained() =>
        new DiscoveryStateBuilder().With(state: "idle", done: 1213, unavailable: 40)
            .WithUpstreamOnly(3);

    public static DiscoveryStateBuilder Paused() =>
        new DiscoveryStateBuilder().With(state: "paused", done: 500, queued: 120);

    public DiscoveryStateBuilder With(
        string? state = null, int? done = null, int? queued = null, int? running = null,
        int? unavailable = null)
    {
        _state = state ?? _state;
        _done = done ?? _done;
        _queued = queued ?? _queued;
        _running = running ?? _running;
        _unavailable = unavailable ?? _unavailable;
        return this;
    }

    public DiscoveryStateBuilder WithUpstreamOnly(int n)
    {
        _upstreamOnly = n;
        return this;
    }

    public DiscoveryStateBuilder WithBuildTime(int n)
    {
        _buildTime = n;
        return this;
    }

    public DiscoveryStateBuilder WithAdvisories(int n)
    {
        _advisories = n;
        return this;
    }

    public DiscoveryStateBuilder WithTiers(int direct, int transitive)
    {
        _direct = direct;
        _transitive = transitive;
        return this;
    }

    /// <summary>A tier that holds work with none of it analyzed, as a requeue leaves it.</summary>
    public DiscoveryStateBuilder WithDirectAnalyzed(int analyzed)
    {
        _directAnalyzed = analyzed;
        return this;
    }

    public DiscoveryStateBuilder WithReach(int pending, int threshold, string recomputedAt)
    {
        _reachPending = pending;
        _reachThreshold = threshold;
        _reachRecomputedAt = recomputedAt;
        return this;
    }

    public string ToJson() => JsonSerializer.Serialize(new
    {
        available = true,
        status = new
        {
            state = _state,
            concurrency = 4,
            reachPending = _reachPending,
            reachThreshold = _reachThreshold,
            claimsPerCycle = 20,
        },
        frontier = new Dictionary<string, int>
        {
            ["done"] = _done,
            ["queued"] = _queued,
            ["retrying"] = 0,
            ["running"] = _running,
            ["unavailable"] = _unavailable,
            ["advisories"] = _advisories,
            ["buildTime"] = _buildTime,
            ["upstreamOnly"] = _upstreamOnly,
        },
        tiers = new Dictionary<string, object>
        {
            ["direct"] = new { total = _direct, analyzed = _directAnalyzed ?? _direct },
            ["transitive"] = new { total = _transitive, analyzed = _transitive },
        },
        version = new { reachRecomputedAt = _reachRecomputedAt },
        advisoriesFetchedAt = "",
    });
}
