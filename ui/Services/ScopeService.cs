namespace Abom.WebAssembly.Services;

/// <summary>
/// Shared address-bar scope: API predicate plus UI-only selection.
/// </summary>
public sealed class ScopeService
{
    private const string ParamSelection = "selection";

    private const string ParamFocus = "focus";

    /// <summary>
    /// UI-only terms that must not be sent to predicate endpoints.
    /// </summary>
    /// <remarks>
    /// Declared rather than inferred so misspelled filters are refused instead of ignored.
    /// </remarks>
    private static readonly HashSet<string> PresentationKeys =
        new(StringComparer.Ordinal)
        {
            "view", "direction", "from", "status", "hops", "tab", "unit",
            "groupBy", "use3d", "collapsed", "stableLayout",
        };

    public ViewSpec Spec { get; private set; } = new();

    /// <summary>The selected node keys, sorted as a set.</summary>
    public IReadOnlyList<string> Selection { get; private set; } = [];

    /// <summary>Why the last address was refused, or null when it was understood.</summary>
    public string? LastError { get; private set; }

    /// <summary>
    /// Whether the last address named this term at all.
    /// </summary>
    /// <remarks>
    /// Distinguishes absent lists from deliberately empty lists, such as <c>kinds=</c>.
    /// </remarks>
    public bool Names(string term) => _named.Contains(term);

    private HashSet<string> _named = new(StringComparer.Ordinal);

    public event Action? Changed;

    public void Set(ViewSpec spec) => Apply(spec ?? new ViewSpec(), Selection);

    public void Select(IEnumerable<string>? keys) => Apply(Spec, Normalize(keys));

    /// <summary>
    /// Predicate and selection as one canonical query string.
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

        // Stable key sort preserves repeated values in writer order.
        return string.Join("&", pairs.OrderBy(KeyOf, StringComparer.Ordinal));
    }

    /// <summary>Reads the scope from a full URI or bare query string.</summary>
    /// <param name="address">A full URI or a bare query string.</param>
    /// <returns>
    /// Whether the address named a scope at all. False lets caller arrival defaults apply.
    /// </returns>
    public bool ReadFrom(string? address)
    {
        LastError = null;
        var at = (address ?? "").IndexOf('?');
        // No '?' means no query; a bare path is not a filter name.
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
            // Refuse the whole address rather than applying a partial scope.
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
