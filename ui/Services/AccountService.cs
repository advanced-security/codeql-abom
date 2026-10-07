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
        CheckAttempted = true;
        Adopt(await api.RefreshAccountsAsync(ct));
    }

    /// <summary>
    /// Whether gh has been asked during this visit to the application. The Settings page asks on
    /// its own only when nothing is on record and this is false, so a broken gh is asked once
    /// rather than on every arrival.
    /// </summary>
    public bool CheckAttempted { get; private set; }

    /// <summary>True when no verification has ever been recorded, as opposed to one that failed.</summary>
    public bool NothingOnRecord => Identity.VerifiedAt is null && !Identity.Authenticated;

    private readonly Dictionary<string, AccountRepositories> _repositories =
        new(StringComparer.OrdinalIgnoreCase);

    /// <summary>
    /// An account's repositories, read once per visit and only when asked for. A listing that
    /// failed is not kept, so asking again after fixing gh asks GitHub again.
    /// </summary>
    public async Task<AccountRepositories> RepositoriesAsync(string owner, CancellationToken ct = default)
    {
        if (_repositories.TryGetValue(owner, out var known)) return known;
        var listed = await api.ListAccountRepositoriesAsync(owner, ct);
        if (listed.Problem is null) _repositories[owner] = listed;
        return listed;
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

    /// <summary>
    /// Whether an enabled declaration names the account, whole or in part. An account-level
    /// question: a repository is yours only where the declaration covers it, which only the server
    /// decides. Scope keys start with the account owner.
    /// </summary>
    public bool IsDeclared(string? scopeKey)
    {
        if (string.IsNullOrEmpty(scopeKey)) return false;
        var slash = scopeKey.IndexOf('/');
        return _owned.Contains(slash < 0 ? scopeKey : scopeKey[..slash]);
    }

    /// <summary>Whether the account is declared only in part: named repositories or property
    /// scopes rather than everything it holds.</summary>
    public bool IsDeclaredInPart(string owner) =>
        Declared?.FirstOrDefault(a => string.Equals(a.Owner, owner, StringComparison.OrdinalIgnoreCase))
            is { IncludesOnly: true };
}
