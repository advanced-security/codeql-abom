namespace Abom.WebAssembly.Services;

/// <summary>How a commit is written where a whole one would not fit.</summary>
public static class CommitText
{
    /// <summary>The first twelve characters; the whole commit belongs in the title beside it.</summary>
    public static string Short(string commit) => commit.Length <= 12 ? commit : commit[..12];

    /// <summary>
    /// A reference as written, except a whole commit, written as commits are. An empty one is a local
    /// path (<c>uses: ./</c>), which runs at its caller's own commit: not the default branch, and not
    /// a version.
    /// </summary>
    public static string Reference(string reference) => reference switch
    {
        "" => "local path",
        { Length: 40 } when reference.All(char.IsAsciiHexDigitLower) => Short(reference),
        _ => reference,
    };
}
