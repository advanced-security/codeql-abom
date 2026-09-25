using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// The query versions a run is being drafted with, one draft for every page that offers Run: the
/// catalog adds to it and the Inventory reads it, so a run started on either is the one drafted.
/// Held in memory only, so a reload never starts a run nobody remembers.
/// </summary>
public sealed class RunDraft
{
    private List<AnalysisQueryRef> _queries = [];

    public IReadOnlyList<AnalysisQueryRef> Queries => _queries;

    public event Action? Changed;

    public void Set(IEnumerable<AnalysisQueryRef> queries)
    {
        _queries = [.. queries];
        Changed?.Invoke();
    }

    /// <summary>Adds a version once: a run names each version at most once.</summary>
    public void Add(AnalysisQueryRef query)
    {
        if (!_queries.Contains(query))
        {
            Set([.. _queries, query]);
        }
    }

    public void Remove(AnalysisQueryRef query) => Set(_queries.Where(q => q != query));

    public void Clear() => Set([]);
}
