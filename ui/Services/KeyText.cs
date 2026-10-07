namespace Abom.WebAssembly.Services;

/// <summary>
/// Identities shortened to the part that distinguishes one row from the next.
///
/// <para>
/// A node key carries everything needed to address the thing, which is why it is the key. Down a
/// column it is mostly a prefix every other row repeats: all 1,221 workflows in the corpus live
/// under <c>.github/workflows/</c>, so eighteen characters of every path say only "this is a
/// workflow" while pushing the counts off the far edge of the table.
/// </para>
/// <para>
/// Nothing shortened here is the only copy. The cell keeps the full value in its tooltip, the link
/// still addresses the node by its whole key, and the repository a thing belongs to is shown
/// rather than dropped. What this may never do is shorten two different things into the same
/// name — hence the refusal to split an action that is a whole repository.
/// </para>
/// </summary>
public static class KeyText
{
    private const string WorkflowDir = ".github/workflows/";
    private const string InsideRepo = "/.github/";
    private const string DigestMarker = "@sha256:";

    /// <summary>How much of a digest is enough to tell two of them apart by eye.</summary>
    private const int DigestChars = 12;

    /// <summary>A reference split into where it lives and what it is called.</summary>
    /// <param name="Repository">The repository it lives in, or "" when the name is the whole thing.</param>
    /// <param name="Name">What distinguishes it. Never empty.</param>
    public readonly record struct Identity(string Repository, string Name);

    /// <summary>A workflow path without the directory every workflow shares.</summary>
    public static string WorkflowFile(string path) =>
        path.StartsWith(WorkflowDir, StringComparison.Ordinal) && path.Length > WorkflowDir.Length
            ? path[WorkflowDir.Length..]
            : path;

    /// <summary>
    /// A reference split for display: "owner/repo/.github/workflows/ci.yml" becomes the repository
    /// and "ci.yml".
    ///
    /// <para>
    /// An action that is a whole repository is returned whole, because its slashes are part of its
    /// name: "actions/checkout" shortened to "checkout" names a different action in a different
    /// account.
    /// </para>
    /// </summary>
    public static Identity Split(string reference)
    {
        if (InsideWorkflow(reference) is { } inside)
        {
            return inside;
        }
        var at = reference.IndexOf(InsideRepo, StringComparison.Ordinal);
        if (at <= 0)
        {
            return InsideAction(reference) ?? new Identity("", Abbreviate(reference));
        }

        var name = reference[(at + InsideRepo.Length)..];
        var lastSlash = name.LastIndexOf('/');
        if (lastSlash >= 0 && lastSlash < name.Length - 1)
        {
            name = name[(lastSlash + 1)..];
        }

        return name.Length == 0
            ? new Identity("", Abbreviate(reference))
            : new Identity(reference[..at], name);
    }

    /// <summary>A stored boundary in the words docs/vocabulary.md chose for it.</summary>
    public static string Boundary(string stored) => stored switch
    {
        "same-repository" => "Same repository",
        "same-owner" => "Same account",
        "cross-owner" => "Another account",
        "" => "not known",
        _ => stored,
    };

    // `owner/repo/path` is an action at a path inside a repository, shown under the repository it
    // lives in, as one under .github/ is. Anything with a scheme or a colon is not one.
    private static Identity? InsideAction(string reference)
    {
        if (reference.Contains(':')) return null;
        var parts = reference.Split('/', 3);
        return parts is [{ Length: > 0 } owner, { Length: > 0 } repo, { Length: > 0 } path]
            ? new Identity($"{owner}/{repo}", path)
            : null;
    }

    // `job:repository:path:job` and `step:repository:path:job:index`, the step counted from one
    // because that is how a reader counts the steps of a job.
    private static Identity? InsideWorkflow(string reference)
    {
        var parts = reference.Split(':');
        return parts switch
        {
            ["job", var repo, var path, var job] =>
                new Identity(repo, $"{WorkflowFile(path)} › {job}"),
            ["step", var repo, var path, var job, var index] when int.TryParse(index, out var i) =>
                new Identity(repo, $"{WorkflowFile(path)} › {job} › step {i + 1}"),
            _ => null,
        };
    }

    /// <summary>
    /// A digest cut to its first few characters. A tag is left alone: it is already the short
    /// form, and abbreviating it would invent a version nobody pinned.
    /// </summary>
    public static string Abbreviate(string reference)
    {
        var at = reference.IndexOf(DigestMarker, StringComparison.Ordinal);
        if (at < 0)
        {
            return reference;
        }

        var start = at + DigestMarker.Length;
        return reference.Length <= start + DigestChars
            ? reference
            : string.Concat(reference.AsSpan(0, start + DigestChars), "\u2026");
    }
}
