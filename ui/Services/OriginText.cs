namespace Abom.WebAssembly.Services;

/// <summary>
/// Whose something is, in the words every surface shows. The declaration decides first; where it
/// does not cover the repository, how the repository arrived does. Being recorded does not mean
/// the operator owns the repository.
/// </summary>
public static class OriginText
{
    /// <summary>owned, starting point, upstream or external.</summary>
    public static string Of(string origin, bool owned) => owned ? "owned" : origin switch
    {
        "seed" => "starting point",
        "upstream" => "upstream",
        _ => "external",
    };
}
