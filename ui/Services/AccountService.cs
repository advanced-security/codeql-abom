using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// What the operator has declared is theirs, and what <c>gh</c> says they belong to.
///
/// The two are kept apart. The declaration decides ownership; the verification is evidence about
/// access, shown beside it. Loaded once and shared, because a page disagreeing with its neighbor
/// about what is yours would be worse than marking nothing at all.
/// </summary>
public sealed class AccountService(AbomApi api)
{
    private HashSet<string> _owned = new(StringComparer.OrdinalIgnoreCase);
    private HashSet<string> _verified = new(StringComparer.OrdinalIgnoreCase);
    private bool _loaded;

    public event Action? Changed;

    public AccountIdentity Identity { get; private set; } = new();

    /// <summary>Null until the declaration has arrived, so "still loading" and "nothing
    /// declared" stay distinguishable — they are opposite claims.</summary>
    public IReadOnlyList<DeclaredAccount>? Declared { get; private set; }

    /// <summary>With nothing declared every figure stays undivided, as it read before the
    /// feature existed.</summary>
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

    /// <summary>Reloads the declaration after it has been edited.</summary>
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

    /// <summary>Every scope key starts with the account, so one test serves them all.</summary>
    public bool IsOwned(string? scopeKey)
    {
        if (string.IsNullOrEmpty(scopeKey)) return false;
        var slash = scopeKey.IndexOf('/');
        return _owned.Contains(slash < 0 ? scopeKey : scopeKey[..slash]);
    }
}
