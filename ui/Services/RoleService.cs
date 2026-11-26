using Abom.WebAssembly.Models;

namespace Abom.WebAssembly.Services;

/// <summary>
/// Who is using ABOM and what their role lets them do, read once from <c>/api/me</c>, so a control
/// the role does not allow is offered disabled with the role it needs, rather than offered and
/// refused on press.
/// </summary>
public sealed class RoleService(AbomApi api)
{
    private Task<Me?>? _me;

    /// <summary>The person asking, or null when it could not be read, which says nothing either way.</summary>
    public Task<Me?> MeAsync() => _me ??= api.ReadMeAsync();

    /// <summary>
    /// Why <paramref name="operation"/> is not offered, or null when it is: the role it needs, which
    /// the person does not hold. Unknown when <c>/api/me</c> could not be read, which offers it, as
    /// the server still decides.
    /// </summary>
    public async Task<string?> RefusalAsync(string operation)
    {
        if (await MeAsync() is not { } me || me.May.Contains(operation))
        {
            return null;
        }
        var needs = me.Needs.TryGetValue(operation, out var role) ? $"the {role} role" : "a role you do not hold";
        return $"This needs {needs}; ask an administrator to assign it.";
    }

    /// <summary>What an operation lets someone do, in words.</summary>
    public static string Words(string operation) => operation switch
    {
        "abom/declare-scope" => "Declare the accounts ABOM covers",
        "abom/discovery-control" => "Start, pause and steer discovery",
        "abom/verify-identity" => "Check who you are on GitHub",
        "abom/lock-verify" => "Verify a repository's lock file",
        "abom/enable-code-scanning" => "Turn on code scanning",
        "abom/configure" => "Change how ABOM is configured",
        "abom/policy" => "Write policies and waivers",
        "abom/prune" => "Remove what the declared scope no longer reaches",
        "abom/submit-snapshot" => "Submit dependency snapshots",
        "abom/export-query" => "Export a query as a pull request",
        "abom/authoring" => "Run query authoring sessions",
        "abom/query-lifecycle" => "Advance a query through its stages",
        "abom/query-request" => "File and edit query requests",
        "abom/query-test" => "Run a query's tests",
        "abom/requested-run" => "Run queries over chosen repositories",
        "abom/saved-view" => "Save named views",
        _ => operation,
    };
}
