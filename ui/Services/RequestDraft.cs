using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// A request prefilled from a run's results, held in memory until the operator files or discards
/// it: asking from a run fills in the form, and filing it stays the operator's act.
/// </summary>
public sealed class RequestDraft
{
    public QueryRequest? Request { get; private set; }

    public void Set(QueryRequest request) => Request = request;

    public void Clear() => Request = null;
}
