using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// The accounts the operator has declared they control.
///
/// Loaded once and shared, because every page needs the same answer and a page disagreeing with
/// its neighbour about what is yours would be worse than not marking anything at all.
/// </summary>
public sealed class OwnedOrgService(AbomApi api)
{
    private HashSet<string> _owned = new(StringComparer.OrdinalIgnoreCase);
    private bool _loaded;

    public event Action? Changed;

    public IReadOnlyCollection<string> Owned => _owned;

    /// <summary>With nothing declared every figure stays undivided, as it was before the
    /// feature existed.</summary>
    public bool AnyDeclared => _owned.Count > 0;

    public async Task EnsureLoadedAsync(CancellationToken ct = default)
    {
        if (_loaded) return;
        _loaded = true;
        _owned = new HashSet<string>(await api.GetOwnedOrgsAsync(ct), StringComparer.OrdinalIgnoreCase);
        Changed?.Invoke();
    }

    public async Task SetAsync(IEnumerable<string> owners, CancellationToken ct = default)
    {
        var stored = await api.SetOwnedOrgsAsync(owners, ct);
        _owned = new HashSet<string>(stored, StringComparer.OrdinalIgnoreCase);
        _loaded = true;
        Changed?.Invoke();
    }

    public async Task ToggleAsync(string owner, CancellationToken ct = default)
    {
        var next = new HashSet<string>(_owned, StringComparer.OrdinalIgnoreCase);
        if (!next.Remove(owner)) next.Add(owner);
        await SetAsync(next, ct);
    }

    /// <summary>Every scope key starts with the account, so one test serves them all.</summary>
    public bool IsOwned(string? scopeKey)
    {
        if (string.IsNullOrEmpty(scopeKey)) return false;
        var slash = scopeKey.IndexOf('/');
        return _owned.Contains(slash < 0 ? scopeKey : scopeKey[..slash]);
    }
}
