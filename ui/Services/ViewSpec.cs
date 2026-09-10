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
/// </summary>
public sealed class ViewSpec
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
    private const string ParamRefStyle = "ref";
    private const string ParamBoundary = "boundary";
    private const string ParamMinReach = "minReach";
    private const string ParamFlags = "flags";
    private const string ParamSteps = "steps";

    /// <summary>
    /// Writes the canonical text: keys ordinally sorted, list values sorted and deduplicated,
    /// defaults omitted. Two specs asking the same question produce the same text.
    /// </summary>
    public string ToQueryString()
    {
        var pairs = new SortedDictionary<string, string>(StringComparer.Ordinal);

        foreach (var (key, value) in new[]
                 {
                     (ParamScope, Scope), (ParamSearch, Search),
                     (ParamRefStyle, RefStyle), (ParamBoundary, Boundary),
                     (ParamProvider, Provider), (ParamSeverity, Severity),
                 })
        {
            if (!string.IsNullOrEmpty(value)) pairs[key] = value;
        }

        foreach (var (key, values) in new[]
                 {
                     (ParamKinds, Kinds), (ParamOrigins, Origins), (ParamRules, Rules),
                     (ParamNotRules, NotRules),
                     (ParamRepos, Repositories),
                     (ParamEdgeClass, EdgeClass), (ParamFlags, Flags),
                 })
        {
            var normalized = Normalize(values);
            if (normalized.Count > 0) pairs[key] = string.Join(",", normalized);
        }

        if (MinReach > 0) pairs[ParamMinReach] = MinReach.ToString(CultureInfo.InvariantCulture);
        if (Steps) pairs[ParamSteps] = "true";

        return string.Join("&", pairs.Select(p => $"{Escape(p.Key)}={Escape(p.Value)}"));
    }

    /// <summary>
    /// Reads the canonical text, refusing anything it does not understand. An unknown or empty
    /// parameter is an error rather than a value to skip: a predicate that quietly ignored a typo
    /// would match everything, and the gate consuming it fails a pull request on a non-empty match.
    /// </summary>
    /// <exception cref="FormatException">The text names something that is not a predicate.</exception>
    public static ViewSpec Parse(string text)
    {
        string scope = "", search = "", refStyle = "", boundary = "", provider = "", severity = "";
        IReadOnlyList<string> kinds = [], origins = [], rules = [], edges = [], flags = [], notRules = [], repositories = [];
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
                    $"view: \"{key}\" has no value; absent and empty are different questions");
            }

            switch (key)
            {
                case ParamScope: scope = raw; break;
                case ParamSearch: search = raw; break;
                case ParamRefStyle: refStyle = raw; break;
                case ParamBoundary: boundary = raw; break;
                case ParamKinds: kinds = Normalize(raw.Split(',')); break;
                case ParamOrigins: origins = Normalize(raw.Split(',')); break;
                case ParamRules: rules = Normalize(raw.Split(',')); break;
                case ParamNotRules: notRules = Normalize(raw.Split(',')); break;
                case ParamRepos: repositories = Normalize(raw.Split(',')); break;
                case ParamProvider: provider = raw; break;
                case ParamSeverity: severity = raw; break;
                case ParamEdgeClass: edges = Normalize(raw.Split(',')); break;
                case ParamFlags: flags = Normalize(raw.Split(',')); break;
                case ParamMinReach:
                    if (!int.TryParse(raw, NumberStyles.Integer, CultureInfo.InvariantCulture,
                            out minReach) || minReach < 0)
                    {
                        throw new FormatException(
                            $"view: {ParamMinReach} must be a whole number, got \"{raw}\"");
                    }
                    break;
                case ParamSteps:
                    if (!bool.TryParse(raw, out steps))
                    {
                        throw new FormatException(
                            $"view: {ParamSteps} must be true or false, got \"{raw}\"");
                    }
                    break;
                default:
                    throw new FormatException($"view: unknown filter \"{key}\"");
            }
        }

        return new ViewSpec
        {
            Scope = scope, Search = search, RefStyle = refStyle, Boundary = boundary,
            Provider = provider, Severity = severity,
            Kinds = kinds, Origins = origins, Rules = rules, NotRules = notRules,
            Repositories = repositories,
            EdgeClass = edges, Flags = flags,
            MinReach = minReach, Steps = steps,
        };
    }

    public override bool Equals(object? obj) =>
        obj is ViewSpec other && ToQueryString() == other.ToQueryString();

    public override int GetHashCode() => ToQueryString().GetHashCode(StringComparison.Ordinal);

    private static IReadOnlyList<string> Normalize(IEnumerable<string> values) =>
        values.Select(v => v.Trim())
            .Where(v => v.Length > 0)
            .Distinct(StringComparer.Ordinal)
            .OrderBy(v => v, StringComparer.Ordinal)
            .ToList();

    // Go's url.Values.Encode is the reference, and it differs from Uri.EscapeDataString in one
    // way that matters: a space becomes '+' rather than "%20".
    private static string Escape(string value)
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

    private static string Unescape(string value)
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
