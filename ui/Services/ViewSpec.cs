using System.Globalization;
using System.Text;

namespace Abom.WebAssembly.Services;

/// <summary>
/// The predicate half of a view: which nodes and edges an operator is asking about.
///
/// <para>
/// Presentation — grouping, collapse, 3D, selection, the neighborhood walk — is deliberately not
/// here. A gate that carried a <c>use3d</c> field would have to ignore it, and a change to what is
/// selected must not hide behind a change to how it is drawn.
/// </para>
/// <para>
/// The canonical form is the query string, and it has to be byte-identical to what Go's
/// <c>internal/view</c> writes, because the same text is read by the browser and by the API.
/// </para>
/// <para>
/// A record, so a caller changing one term writes `with` rather than re-listing the other sixteen
/// — which is how a term added later gets silently dropped by whoever forgot to copy it.
/// </para>
/// </summary>
public sealed record ViewSpec
{
    public string Scope { get; init; } = "";
    public string Search { get; init; } = "";
    public IReadOnlyList<string> Kinds { get; init; } = [];
    public IReadOnlyList<string> Origins { get; init; } = [];
    public IReadOnlyList<string> Rules { get; init; } = [];

    /// <summary>
    /// Drops a node carrying any of these at all, rather than dropping the finding and keeping the
    /// node: "A but not C" describes the action or workflow, not the result set.
    /// </summary>
    public IReadOnlyList<string> NotRules { get; init; } = [];

    /// <summary>The SARIF tool driver name, verbatim. Never matches a finding nobody attributed.</summary>
    public string Provider { get; init; } = "";

    public string Severity { get; init; } = "";

    /// <summary>
    /// Narrows to what a caller is asking about — a pull request's repository, for the gate.
    /// Compared case-insensitively wherever it is applied.
    /// </summary>
    public IReadOnlyList<string> Repositories { get; init; } = [];
    public IReadOnlyList<string> EdgeClass { get; init; } = [];
    public string RefStyle { get; init; } = "";
    public string Boundary { get; init; } = "";

    /// <summary>
    /// The analysis shape the databases behind an answer were built in. Empty is <c>regular</c>,
    /// so a reader who never spells it keeps the totals they always had. There is deliberately no
    /// word here for both at once: a union across shapes is a decision something makes on
    /// purpose, never something a shared link carries.
    /// </summary>
    public string Mode { get; init; } = "";

    /// <summary>
    /// Repositories carrying this custom property, which is how a business unit or an application
    /// is named. The key travels with the value: carrying the value alone filtered by whichever
    /// property each server happened to be configured with, so one shared link asked two questions.
    /// </summary>
    public IReadOnlyList<PropertyMatch> Properties { get; init; } = [];

    /// <summary>
    /// Drops an owned node where it is a <em>source</em>. A node an included property still reaches
    /// stays, as a dependency: excluding part of an organization must not sever the supply chain
    /// of a part that was kept.
    /// </summary>
    public IReadOnlyList<PropertyMatch> NotProperties { get; init; } = [];

    /// <summary>
    /// What a relationship is: a step using an action, a job ordered after another, a job calling
    /// a reusable workflow, a composite step using an action. Separate from <see cref="EdgeClass"/>,
    /// which is what the relationship crosses.
    /// </summary>
    public IReadOnlyList<string> EdgeKinds { get; init; } = [];

    /// <summary>A floor. Zero means the question was not asked, not asked about zero.</summary>
    public int MinReach { get; init; }

    public IReadOnlyList<string> Flags { get; init; } = [];

    /// <summary>
    /// Keeps every individual <c>uses:</c> line rather than folding it into its container. It
    /// changes which nodes exist, so it is a predicate rather than a drawing choice.
    /// </summary>
    public bool Steps { get; init; }

    private const string ParamScope = "scope";
    private const string ParamSearch = "q";
    private const string ParamKinds = "kinds";
    private const string ParamOrigins = "origins";
    private const string ParamRules = "rules";
    private const string ParamNotRules = "notRules";
    private const string ParamRepos = "repositories";
    private const string ParamProvider = "provider";
    private const string ParamSeverity = "severity";
    private const string ParamEdgeClass = "edges";
    private const string ParamEdgeKinds = "edgeKinds";
    private const string ParamRefStyle = "ref";
    private const string ParamBoundary = "boundary";
    private const string ParamMode = "mode";
    private const string ParamMinReach = "minReach";
    private const string ParamFlags = "flags";
    private const string ParamSteps = "steps";
    private const string ParamProperties = "properties";
    private const string ParamNotProperties = "notProperties";

    // Only the first one divides, so a value may contain it; GitHub property names cannot.
    private const char PropertySeparator = ':';

    // The terms whose values are fixed in code rather than read from the corpus, mirroring
    // `internal/view`. An unrecognized one used to be kept and then filter nothing: the server's
    // `boundaryFilter` drops a value it does not know rather than letting it reach the SQL, and
    // `scopeFilter` falls through to no scope at all.
    private static readonly Dictionary<string, string[]> Vocabulary = new()
    {
        [ParamScope] = ["owned", "exposure"],
        [ParamBoundary] = ["same-repository", "same-owner", "cross-owner"],
        [ParamRefStyle] = ["any", "sha", "tag"],
        [ParamMode] = ["regular", "extended"],
    };

    // The near-misses are real: the canvas draws `crossOwner` where the store spells it
    // `cross-owner`, so the refusal lists what would have worked.
    private static string Vetted(string term, string value)
    {
        if (!Vocabulary.TryGetValue(term, out var allowed) || allowed.Contains(value))
        {
            return value;
        }
        throw new FormatException(
            $"{term} does not take \"{value}\"; it takes {string.Join(", ", allowed)}");
    }

    /// <summary>
    /// Writes the canonical text: keys ordinally sorted, list values sorted and deduplicated,
    /// defaults omitted. Two specs asking the same question produce the same text.
    /// </summary>
    public string ToQueryString()
    {
        // A list rather than a dictionary because a property term repeats its key, and Go's
        // url.Values.Encode sorts by key while keeping each key's values in the order added.
        var pairs = new List<(string Key, string Value)>();

        foreach (var (key, value) in new[]
                 {
                     (ParamScope, Scope), (ParamSearch, Search),
                     (ParamRefStyle, RefStyle), (ParamBoundary, Boundary),
                     (ParamProvider, Provider), (ParamSeverity, Severity),
                     (ParamMode, Mode),
                 })
        {
            if (!string.IsNullOrEmpty(value)) pairs.Add((key, value));
        }

        foreach (var (key, values) in new[]
                 {
                     (ParamKinds, Kinds), (ParamOrigins, Origins), (ParamRules, Rules),
                     (ParamNotRules, NotRules),
                     (ParamRepos, Repositories),
                     (ParamEdgeClass, EdgeClass), (ParamEdgeKinds, EdgeKinds),
                     (ParamFlags, Flags),
                 })
        {
            var normalized = Normalize(values);
            if (normalized.Count > 0) pairs.Add((key, string.Join(",", normalized)));
        }

        // One parameter per match rather than a comma-joined list: a property value is operator
        // text, and a value containing a comma must not become two values.
        foreach (var (key, matches) in new[]
                 {
                     (ParamProperties, Properties), (ParamNotProperties, NotProperties),
                 })
        {
            foreach (var match in NormalizeMatches(matches)) pairs.Add((key, match.ToString()));
        }

        if (MinReach > 0) pairs.Add((ParamMinReach, MinReach.ToString(CultureInfo.InvariantCulture)));
        if (Steps) pairs.Add((ParamSteps, "true"));

        return string.Join("&", pairs
            .OrderBy(p => p.Key, StringComparer.Ordinal)
            .Select(p => $"{Escape(p.Key)}={Escape(p.Value)}"));
    }

    /// <summary>
    /// Reads the canonical text, refusing anything it does not understand. An unknown or empty
    /// parameter is an error rather than a value to skip: a predicate that quietly ignored a typo
    /// would match everything, and the gate consuming it fails a pull request on a non-empty match.
    /// </summary>
    /// <exception cref="FormatException">The text names something that is not a predicate.</exception>
    public static ViewSpec Parse(string text)
    {
        string scope = "", search = "", refStyle = "", boundary = "", provider = "", severity = "",
            mode = "";
        IReadOnlyList<string> kinds = [], origins = [], rules = [], edges = [], edgeKinds = [],
            flags = [], notRules = [], repositories = [];
        List<PropertyMatch> properties = [], notProperties = [];
        var minReach = 0;
        var steps = false;

        foreach (var part in (text ?? "").TrimStart('?').Split('&', StringSplitOptions.RemoveEmptyEntries))
        {
            var split = part.IndexOf('=');
            var key = Unescape(split < 0 ? part : part[..split]);
            var raw = split < 0 ? "" : Unescape(part[(split + 1)..]);
            if (raw.Length == 0)
            {
                throw new FormatException(
                    $"\"{key}\" has no value; absent and empty are different questions");
            }

            switch (key)
            {
                case ParamScope: scope = Vetted(key, raw); break;
                case ParamSearch: search = raw; break;
                case ParamRefStyle: refStyle = Vetted(key, raw); break;
                case ParamBoundary: boundary = Vetted(key, raw); break;
                case ParamMode: mode = Vetted(key, raw); break;
                case ParamKinds: kinds = Normalize(raw.Split(',')); break;
                case ParamOrigins: origins = Normalize(raw.Split(',')); break;
                case ParamRules: rules = Normalize(raw.Split(',')); break;
                case ParamNotRules: notRules = Normalize(raw.Split(',')); break;
                case ParamRepos: repositories = Normalize(raw.Split(',')); break;
                case ParamProvider: provider = raw; break;
                case ParamSeverity: severity = raw; break;
                case ParamEdgeClass: edges = Normalize(raw.Split(',')); break;
                case ParamEdgeKinds: edgeKinds = Normalize(raw.Split(',')); break;
                case ParamFlags: flags = Normalize(raw.Split(',')); break;
                case ParamProperties: properties.Add(ParseMatch(key, raw)); break;
                case ParamNotProperties: notProperties.Add(ParseMatch(key, raw)); break;
                case ParamMinReach:
                    if (!int.TryParse(raw, NumberStyles.Integer, CultureInfo.InvariantCulture,
                            out minReach) || minReach < 0)
                    {
                        throw new FormatException(
                            $"{ParamMinReach} must be a whole number, got \"{raw}\"");
                    }
                    break;
                case ParamSteps:
                    if (!bool.TryParse(raw, out steps))
                    {
                        throw new FormatException(
                            $"{ParamSteps} must be true or false, got \"{raw}\"");
                    }
                    break;
                default:
                    throw new FormatException($"ABOM has no filter called \"{key}\"");
            }
        }

        return new ViewSpec
        {
            Scope = scope, Search = search, RefStyle = refStyle, Boundary = boundary,
            Provider = provider, Severity = severity, Mode = mode,
            Kinds = kinds, Origins = origins, Rules = rules, NotRules = notRules,
            Repositories = repositories,
            Properties = properties, NotProperties = notProperties,
            EdgeClass = edges, EdgeKinds = edgeKinds, Flags = flags,
            MinReach = minReach, Steps = steps,
        };
    }

    // Either half missing is refused rather than defaulted: a term carrying only a value would
    // match against whichever property the server happened to be configured with.
    private static PropertyMatch ParseMatch(string key, string raw)
    {
        var at = raw.IndexOf(PropertySeparator);
        var name = at < 0 ? "" : raw[..at].Trim();
        var value = at < 0 ? "" : raw[(at + 1)..].Trim();
        if (name.Length == 0 || value.Length == 0)
        {
            throw new FormatException(
                $"{key} must be name{PropertySeparator}value, got \"{raw}\"");
        }
        return new PropertyMatch(name, value);
    }

    // Declared rather than left to the record, whose generated equality compares list instances by
    // reference: two specs asking the same question are the same question.
    public bool Equals(ViewSpec? other) =>
        other is not null && ToQueryString() == other.ToQueryString();

    public override int GetHashCode() => ToQueryString().GetHashCode(StringComparison.Ordinal);

    private static IReadOnlyList<string> Normalize(IEnumerable<string> values) =>
        values.Select(v => v.Trim())
            .Where(v => v.Length > 0)
            .Distinct(StringComparer.Ordinal)
            .OrderBy(v => v, StringComparer.Ordinal)
            .ToList();

    private static IReadOnlyList<PropertyMatch> NormalizeMatches(
        IEnumerable<PropertyMatch> matches) =>
        matches.Where(m => m.Name.Length > 0 && m.Value.Length > 0)
            .Distinct()
            .OrderBy(m => m.Name, StringComparer.Ordinal)
            .ThenBy(m => m.Value, StringComparer.Ordinal)
            .ToList();

    // Go's url.Values.Encode is the reference, and it differs from Uri.EscapeDataString in one
    // way that matters: a space becomes '+' rather than "%20".
    internal static string Escape(string value)
    {
        var builder = new StringBuilder(value.Length);
        foreach (var b in Encoding.UTF8.GetBytes(value))
        {
            var c = (char)b;
            if (char.IsAsciiLetterOrDigit(c) || c is '-' or '_' or '.' or '~') builder.Append(c);
            else if (c == ' ') builder.Append('+');
            else builder.Append('%').Append(b.ToString("X2", CultureInfo.InvariantCulture));
        }
        return builder.ToString();
    }

    internal static string Unescape(string value)
    {
        var bytes = new List<byte>(value.Length);
        for (var i = 0; i < value.Length; i++)
        {
            switch (value[i])
            {
                case '+':
                    bytes.Add((byte)' ');
                    break;
                case '%' when i + 2 < value.Length:
                    bytes.Add(Convert.ToByte(value.Substring(i + 1, 2), 16));
                    i += 2;
                    break;
                default:
                    bytes.AddRange(Encoding.UTF8.GetBytes([value[i]]));
                    break;
            }
        }
        return Encoding.UTF8.GetString([.. bytes]);
    }
}

/// <summary>
/// One custom property key and one value a repository must carry for it.
/// </summary>
/// <remarks>
/// A record, so two matches naming the same thing are equal and deduplicate — the canonical text
/// is a storage key, and the order somebody happened to click in must not produce a second text.
/// </remarks>
public sealed record PropertyMatch(string Name, string Value)
{
    public override string ToString() => $"{Name}:{Value}";
}
