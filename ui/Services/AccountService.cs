using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// The accounts the operator is verified to belong to.
///
/// Loaded once and shared, because every page needs the same answer and a page disagreeing with
/// its neighbor about what is yours would be worse than marking nothing at all.
/// </summary>
public sealed class AccountService(AbomApi api)
{
    private HashSet<string> _owned = new(StringComparer.OrdinalIgnoreCase);
    private bool _loaded;

    public event Action? Changed;

    public AccountIdentity Identity { get; private set; } = new();

    /// <summary>With nothing verified every figure stays undivided, as it read before the
    /// feature existed.</summary>
    public bool AnyVerified => _owned.Count > 0;

    public async Task EnsureLoadedAsync(CancellationToken ct = default)
    {
        if (_loaded) return;
        _loaded = true;
        Adopt(await api.GetAccountsAsync(ct));
    }

    public async Task RefreshAsync(CancellationToken ct = default)
    {
        _loaded = true;
        Adopt(await api.RefreshAccountsAsync(ct));
    }

    private void Adopt(AccountIdentity identity)
    {
        Identity = identity;
        _owned = new HashSet<string>(
            identity.Accounts.Select(a => a.Owner), StringComparer.OrdinalIgnoreCase);
        Changed?.Invoke();
    }

    /// <summary>Every scope key starts with the account, so one test serves them all.</summary>
    public bool IsOwned(string? scopeKey)
    {
        if (string.IsNullOrEmpty(scopeKey)) return false;
        var slash = scopeKey.IndexOf('/');
        return _owned.Contains(slash < 0 ? scopeKey : scopeKey[..slash]);
    }
}
