namespace Abom.WebAssembly.Services;

/// <summary>
/// One scope: the predicate that says which things match, and the selection that says which of
/// them an action is about.
///
/// <para>
/// The address bar is its canonical form, so a scope is shareable by construction and a surface
/// has nowhere else to keep one. Two surfaces holding their own copies is how the same words came
/// to mean different things on the table and on the graph.
/// </para>
/// <para>
/// The predicate half stays byte-identical to what Go's <c>internal/view</c> writes; the selection
/// rides alongside it and is never sent to an endpoint that refuses unknown terms.
/// </para>
/// </summary>
public sealed class ScopeService
{
    private const string ParamSelection = "selection";

    // What every page linking into the Explorer calls the same thing. Read, never written, so one
    // spelling leaves here.
    private const string ParamFocus = "focus";

    /// <summary>
    /// Terms that describe how a scope is drawn rather than what it contains.
    /// </summary>
    /// <remarks>
    /// Declared rather than inferred, because the alternative is treating anything unrecognized as
    /// presentation — which is exactly how a misspelled filter comes to match everything. A knob
    /// added to a surface and not added here is refused by name on the next reload, loudly.
    /// </remarks>
    private static readonly HashSet<string> PresentationKeys =
        new(StringComparer.Ordinal)
        { "view", "direction", "from", "status", "hops", "tab", "unit" };

    public ViewSpec Spec { get; private set; } = new();

    /// <summary>The node keys an action would be about. A set, so the order of clicking is not a question.</summary>
    public IReadOnlyList<string> Selection { get; private set; } = [];

    /// <summary>Why the last address was refused, or null when it was understood.</summary>
    public string? LastError { get; private set; }

    /// <summary>
    /// Whether the last address named this term at all.
    /// </summary>
    /// <remarks>
    /// A list the language cannot distinguish from an absent one — <c>kinds</c> holds nothing
    /// either way — so a surface with a non-empty arrival default has no other way to tell a
    /// partial hand-off from a deliberate emptying. Most links in are partial: Discovery sends a
    /// node and a direction and nothing else.
    /// </remarks>
    public bool Names(string term) => _named.Contains(term);

    private HashSet<string> _named = new(StringComparer.Ordinal);

    public event Action? Changed;

    public void Set(ViewSpec spec) => Apply(spec ?? new ViewSpec(), Selection);

    public void Select(IEnumerable<string>? keys) => Apply(Spec, Normalize(keys));

    /// <summary>
    /// Predicate and selection as one text, keys ordinally sorted. Canonical: two scopes asking
    /// the same question about the same things produce the same string.
    /// </summary>
    public string ToQueryString()
    {
        var pairs = new List<string>();
        var predicate = Spec.ToQueryString();
        if (predicate.Length > 0) pairs.AddRange(predicate.Split('&'));
        foreach (var key in Selection)
        {
            pairs.Add($"{ParamSelection}={ViewSpec.Escape(key)}");
        }

        // A stable sort on the key alone, so repeated keys keep the order their writer chose —
        // the same rule Go's url.Values.Encode follows.
        return string.Join("&", pairs.OrderBy(KeyOf, StringComparer.Ordinal));
    }

    /// <summary>
    /// Takes the address as the truth.
    /// </summary>
    /// <param name="address">A full URI or a bare query string.</param>
    /// <returns>
    /// Whether the address named a scope at all. False means it said nothing, so the caller's
    /// arrival defaults apply; true means it said this and only this, including a list somebody
    /// deliberately emptied. Absent and empty are different questions.
    /// </returns>
    public bool ReadFrom(string? address)
    {
        LastError = null;
        var at = (address ?? "").IndexOf('?');
        // No '?' is no query, not a query that happens to be the whole address: without this the
        // path itself parsed as a filter, and every plain visit to /explore was refused by name.
        var query = at < 0 ? "" : address![(at + 1)..];

        var predicate = new List<string>();
        var selection = new List<string>();
        var named = new HashSet<string>(StringComparer.Ordinal);
        foreach (var part in query.Split('&', StringSplitOptions.RemoveEmptyEntries))
        {
            var split = part.IndexOf('=');
            var key = split < 0 ? part : part[..split];
            var raw = split < 0 ? "" : part[(split + 1)..];
            if (PresentationKeys.Contains(key)) continue;
            if (key is ParamSelection or ParamFocus)
            {
                selection.Add(ViewSpec.Unescape(raw));
                named.Add(ParamSelection);
                continue;
            }
            predicate.Add(part);
            named.Add(key);
        }

        if (predicate.Count == 0 && selection.Count == 0)
        {
            _named = named;
            Apply(new ViewSpec(), []);
            return false;
        }

        ViewSpec spec;
        try
        {
            spec = ViewSpec.Parse(string.Join("&", predicate));
        }
        catch (FormatException ex)
        {
            // Left as it was rather than half-applied: a scope assembled from whichever terms
            // happened to parse before the bad one is a question nobody asked.
            LastError = ex.Message;
            return false;
        }

        _named = named;
        Apply(spec, Normalize(selection));
        return true;
    }

    private void Apply(ViewSpec spec, IReadOnlyList<string> selection)
    {
        var before = ToQueryString();
        Spec = spec;
        Selection = selection;
        if (ToQueryString() != before) Changed?.Invoke();
    }

    private static IReadOnlyList<string> Normalize(IEnumerable<string>? keys) =>
        (keys ?? [])
        .Select(k => k.Trim())
        .Where(k => k.Length > 0)
        .Distinct(StringComparer.Ordinal)
        .OrderBy(k => k, StringComparer.Ordinal)
        .ToList();

    private static string KeyOf(string pair)
    {
        var at = pair.IndexOf('=');
        return at < 0 ? pair : pair[..at];
    }
}
