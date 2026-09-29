namespace Abom.WebAssembly.Services;

/// <summary>
/// What a reader calls each surface a term is applied on, and the sentence that says a surface
/// cannot apply one. One place, so a named scope's refusal and a chip's say it in the same words.
/// </summary>
public static class RefusalText
{
    public static readonly IReadOnlyList<(string Surface, string Name)> Pages =
    [
        ("inventory", "Actions"), ("workflows", "Workflows"), ("jobs", "Jobs"),
        ("repositories", "Repositories"), ("advisories", "Advisories"),
        ("relationships", "Relationships"), ("coverage", "Coverage"), ("graph", "the Explorer"),
    ];

    /// <summary>The surface as the subject of a sentence.</summary>
    public static string Here(string surface) => surface switch
    {
        "graph" => "The Explorer",
        "run" => "A run",
        _ => Pages.FirstOrDefault(p => p.Surface == surface).Name ?? "This page",
    };

    /// <summary>A term and its values as a sentence quotes them.</summary>
    public static string Quoted(string label, string values) => $"“{label}: {values}”";

    /// <summary>Why a surface is not narrowed by the quoted terms.</summary>
    public static string CannotNarrow(string surface, IReadOnlyList<string> quoted) =>
        $"{Here(surface)} cannot narrow by {Join(quoted, "or")}.";

    public static string Join(IReadOnlyList<string> items, string last) => items.Count switch
    {
        1 => items[0],
        _ => $"{string.Join(", ", items.Take(items.Count - 1))} {last} {items[^1]}",
    };

    public static string Capitalized(string text) =>
        text.Length == 0 ? text : char.ToUpperInvariant(text[0]) + text[1..];
}
