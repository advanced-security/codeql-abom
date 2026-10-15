using System.Text.Json;

namespace Abom.WebAssembly.Models;

public sealed record AuditCapture
{
    public long ArtifactId { get; init; }
    public int Version { get; init; }
    public string RuntimeSessionId { get; init; } = "";
    public string CallId { get; init; } = "";
    public string Correlation { get; init; } = "";
    public SessionPolicyRecord? Policy { get; init; }
    public AuditTestEvidence? Test { get; init; }
    public Dictionary<string, string> Inputs { get; init; } = [];
}

public sealed record AuditTestEvidence
{
    public string QueryId { get; init; } = "";
    public string QueryVersion { get; init; } = "";
    public string CliVersion { get; init; } = "";
    public bool? Passed { get; init; }
    public int? Total { get; init; }
    public int? Failed { get; init; }
    public int? Missing { get; init; }
    public int? Unexpected { get; init; }
    public string FailureStage { get; init; } = "";
    public string Detail { get; init; } = "";
    public string RanAt { get; init; } = "";
    public string TestDigest { get; init; } = "";
    public string FixturesDigest { get; init; } = "";
    [System.Text.Json.Serialization.JsonExtensionData]
    public Dictionary<string, JsonElement>? Additional { get; init; }
}

public sealed record SessionPolicyRecord
{
    public int Version { get; init; }
    public string Purpose { get; init; } = "";
    public long TimeoutMs { get; init; }
    public string TimeoutSource { get; init; } = "";
    public string Deadline { get; init; } = "";
    public bool CreditsKnown { get; init; }
    public double? MaxAiCredits { get; init; }
    public string Model { get; init; } = "";
    public IReadOnlyList<string> Tools { get; init; } = [];
    public IReadOnlyList<string> Hooks { get; init; } = [];
    public int McpTimeoutMs { get; init; }
    public Dictionary<string, string> Runtime { get; init; } = [];
    public SessionBounds Bounds { get; init; } = new();
    public string Enforcement { get; init; } = "";
}

public sealed record SessionBounds
{
    public string Target { get; init; } = "";
    public Dictionary<string, string> Writable { get; init; } = [];
    public string FixtureRoot { get; init; } = "";
    public IReadOnlyList<string> FixtureExtensions { get; init; } = [];
    public IReadOnlyList<string> ExcludedNames { get; init; } = [];
    public IReadOnlyList<string> ReadOnly { get; init; } = [];
    public IReadOnlyList<string> Denied { get; init; } = [];
    public string QueryHeld { get; init; } = "";
    public Dictionary<string, string[]> ToolArguments { get; init; } = [];
}

public sealed record AuditSession
{
    public Dictionary<string, int> Observed { get; init; } = [];
    public long RowId { get; init; }
    public long RequestId { get; init; }
    public long RunId { get; init; }
    public string QueryId { get; init; } = "";
    public string State { get; init; } = "";
    public string Purpose { get; init; } = "";
    public string StartedBy { get; init; } = "";
    public string Model { get; init; } = "";
    public string StartedAt { get; init; } = "";
    public string EndedAt { get; init; } = "";
    public int Events { get; init; }
    public int Refused { get; init; }

    /// <summary>What it spent, each null until the CLI reported it, which is not zero.</summary>
    public long? ModelCalls { get; init; }
    public long? InputTokens { get; init; }
    public long? OutputTokens { get; init; }
    public long? NanoAiu { get; init; }
    public string Detail { get; init; } = "";
    public string Coverage { get; init; } = "";
    public IReadOnlyList<AuthoringEvent> Policies { get; init; } = [];
    public IReadOnlyList<AuditRelation> Relations { get; init; } = [];
}

public sealed record AuditRelation
{
    public string Kind { get; init; } = "";
    public long RequestId { get; init; }
    public long RunId { get; init; }
    public string QueryId { get; init; } = "";
    public string Version { get; init; } = "";
}

public sealed record AuditSessionList
{
    public IReadOnlyList<AuditSession> Items { get; init; } = [];
    public int Total { get; init; }
}

public sealed record Reproduction
{
    public AuditSession Session { get; init; } = new();
    public AuthoringEventPage Evidence { get; init; } = new();
    public string Limitations { get; init; } = "";
}
