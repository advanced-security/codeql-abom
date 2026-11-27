using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// Whether the declaration on record is one a signed decision allowed, read once for every surface
/// that counts what is yours. Until it is confirmed the ownership predicate honors none of it, so
/// each of those figures reads empty; this is what lets them say why instead.
/// </summary>
public sealed class DeclarationService(AbomApi api)
{
    private Task<ScopeDecision?>? _reading;

    /// <summary>Raised when the decision is read again, after a save or a confirmation.</summary>
    public event Action? Changed;

    /// <summary>
    /// The decision on record, or null when it could not be read, which says nothing either way. A
    /// failed read is tried again by the next caller.
    /// </summary>
    public Task<ScopeDecision?> ReadAsync()
    {
        if (_reading is null || _reading is { IsCompletedSuccessfully: true, Result: null } || _reading.IsFaulted)
        {
            _reading = api.ReadScopeDecisionAsync();
        }
        return _reading;
    }

    /// <summary>Reads the decision again, as a save or a confirmation has just changed it.</summary>
    public async Task<ScopeDecision?> RefreshAsync()
    {
        _reading = api.ReadScopeDecisionAsync();
        var decision = await _reading;
        Changed?.Invoke();
        return decision;
    }

    /// <summary>Whether a declaration is on record that no signed decision, as it now reads, allowed.</summary>
    public static bool Unconfirmed(ScopeDecision? decision) =>
        decision is { State: "unsigned" or "unverified" };
}

/// <summary>
/// How a figure bounded to the declaration says it is waiting on one, in the same words on every
/// surface. "None" is what nothing declared reads as; this is a declaration nobody confirmed.
/// </summary>
public static class DeclarationWords
{
    public const string NotConfirmed = "not confirmed";

    public const string Waiting =
        "Your declaration is not confirmed, so nothing counts as yours and discovery does not act on it.";
}
