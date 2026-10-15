namespace Abom.WebAssembly.Services;

/// <summary>The audit URL contract, shared by filters, scoped entry points and downloads.</summary>
public sealed record AuditSpec
{
    public static readonly string[] Filters =
        ["text", "request", "run", "session", "query", "version", "purpose", "hook", "tool", "decision", "step", "actor", "from", "to", "event", "order"];
    public Dictionary<string, string> Values { get; init; } = [];
    public static string Label(string key) => key switch
    {
        "text" => "Search events", "request" => "Request number", "run" => "Run number",
        "session" => "Session number", "query" => "Query ID", "version" => "Query version",
        "purpose" => "Session purpose", "hook" => "Event kind", "tool" => "Tool name",
        "decision" => "Outcome", "step" => "Recorded step", "actor" => "Actor",
        "from" => "From (date and time)", "to" => "Until (date and time)", "event" => "Event number",
        "order" => "Event order", "limit" => "Page size", "through" => "Snapshot cutoff", _ => key
    };
    public static IReadOnlyList<string> Choices(string key) => key switch
    {
        "purpose" => ["authoring", "sealed drafting"],
        "order" => ["desc", "asc"],
        "decision" => ["allowed", "refused", "succeeded", "failed", "noted", "finished", "stopped", "interrupted"],
        "hook" => ["pre-tool-use", "permission", "post-tool-use", "post-tool-use-failure", "pre-mcp-tool-call",
            "tool-start", "tool-complete", "custom-tool-start", "custom-tool-complete", "test-evidence", "policy",
            "message", "prompt-submitted", "prompt-transformed", "agent-stop", "session-start", "session-end",
            "abom-session-end", "mcp-server", "artifact", "error"],
        _ => []
    };
    public static string ChoiceLabel(string value) => value switch
    {
        "" => "Any", "asc" => "Oldest first", "desc" => "Newest first",
        "authoring" => "Query authoring", _ => AuditText.Words(value)
    };
    public string this[string key] => Values.GetValueOrDefault(key, "");
    public AuditSpec With(string key, string value)
    {
        var copy = new Dictionary<string, string>(Values);
        if (value.Length == 0) copy.Remove(key); else copy[key] = value;
        return this with { Values = copy };
    }
    public string Query => string.Join("&", Values.OrderBy(p => p.Key, StringComparer.Ordinal)
        .Select(p => $"{ViewSpec.Escape(p.Key)}={ViewSpec.Escape(p.Value)}"));
    public string Href => "/codeql?tab=audit" + (Query.Length > 0 ? "&" + Query : "");
    public static string ForSession(long id) => new AuditSpec().With("session", id.ToString()).Href;
    public static string ForRequest(long id) => new AuditSpec().With("request", id.ToString()).Href;
    public static string ForRun(long id) => new AuditSpec().With("run", id.ToString()).Href;
    public static AuditSpec Parse(string query)
    {
        var values = new Dictionary<string, string>();
        foreach (var parts in query.TrimStart('?').Split('&', StringSplitOptions.RemoveEmptyEntries)
                     .Select(pair => pair.Split('=', 2)))
        {
            var key = ViewSpec.Unescape(parts[0]);
            if (key == "tab") continue;
            if (!values.TryAdd(key, parts.Length == 2 ? ViewSpec.Unescape(parts[1]) : ""))
                throw new FormatException($"Duplicate audit term: {key}");
        }
        return new AuditSpec { Values = values };
    }
}
