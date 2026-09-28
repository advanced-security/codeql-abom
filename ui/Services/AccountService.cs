using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// Declared ownership and verified account access, kept separate for rendering.
/// </summary>
public sealed class AccountService(AbomApi api)
{
    private HashSet<string> _owned = new(StringComparer.OrdinalIgnoreCase);
    private HashSet<string> _verified = new(StringComparer.OrdinalIgnoreCase);
    private bool _loaded;

    public event Action? Changed;

    public AccountIdentity Identity { get; private set; } = new();

    /// <summary>Null until declarations load, so loading and none declared stay distinct.</summary>
    public IReadOnlyList<DeclaredAccount>? Declared { get; private set; }

    public bool AnyVerified => _owned.Count > 0;

    /// <summary>False when nothing is declared and enabled, which is what stops discovery.</summary>
    public bool ScopeDeclared => _owned.Count > 0;

    public async Task EnsureLoadedAsync(CancellationToken ct = default)
    {
        if (_loaded) return;
        _loaded = true;
        Adopt(await api.GetAccountsAsync(ct));
        AdoptScope(await api.GetOwnedScopeAsync(ct));
    }

    public async Task RefreshAsync(CancellationToken ct = default)
    {
        _loaded = true;
        Adopt(await api.RefreshAccountsAsync(ct));
    }

    public async Task ReloadScopeAsync(CancellationToken ct = default) =>
        AdoptScope(await api.GetOwnedScopeAsync(ct));

    /// <summary>Whether <c>gh</c> confirms the operator belongs to an account they declared. A
    /// declaration it cannot confirm is kept and flagged, never dropped.</summary>
    public bool IsVerified(string? owner) =>
        !string.IsNullOrEmpty(owner) && _verified.Contains(owner);

    private void Adopt(AccountIdentity identity)
    {
        Identity = identity;
        _verified = new HashSet<string>(
            identity.Accounts.Select(a => a.Owner), StringComparer.OrdinalIgnoreCase);
        Changed?.Invoke();
    }

    private void AdoptScope(IReadOnlyList<DeclaredAccount> declared)
    {
        Declared = declared;
        _owned = new HashSet<string>(
            declared.Where(a => a.Enabled).Select(a => a.Owner), StringComparer.OrdinalIgnoreCase);
        Changed?.Invoke();
    }

    /// <summary>Scope keys start with the account owner.</summary>
    public bool IsOwned(string? scopeKey)
    {
        if (string.IsNullOrEmpty(scopeKey)) return false;
        var slash = scopeKey.IndexOf('/');
        return _owned.Contains(slash < 0 ? scopeKey : scopeKey[..slash]);
    }
}
