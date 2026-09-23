using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>Held in memory only, so a reload never starts a run nobody remembers.</summary>
public sealed class RunDraft
{
    private List<AnalysisQueryRef> _queries = [];

    public IReadOnlyList<AnalysisQueryRef> Queries => _queries;

    public void Set(IEnumerable<AnalysisQueryRef> queries) => _queries = [.. queries];

    public void Clear() => _queries = [];
}
