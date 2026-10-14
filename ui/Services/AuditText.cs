using Abom.WebAssembly.Models;
using MudBlazor;

namespace Abom.WebAssembly.Services;

/// <summary>Presentation of recorded values only; never adjudicates progress or permissions.</summary>
public static class AuditText
{
    public static string Date(string timestamp) => DateTimeOffset.TryParse(timestamp, out var value)
        ? value.ToLocalTime().ToString("dd MMM yyyy") : "Unknown date";
    public static string Words(string value)
    {
        var text = value.Replace('-', ' ').Replace('_', ' ').Replace('.', ' ');
        return text.Length == 0 ? "Not recorded" : char.ToUpperInvariant(text[0]) + text[1..];
    }
    public static string Kind(string hook) => hook switch
    {
        "pre-tool-use" => "Tool permission check",
        "post-tool-use" => "Tool returned",
        "post-tool-use-failure" => "Tool failed",
        "pre-mcp-tool-call" => "MCP permission check",
        "permission" => "Permission decision",
        "policy" => "Session policy recorded",
        "test-evidence" => "Measured test",
        "prompt-submitted" => "Prompt submitted",
        "prompt-transformed" => "Prompt prepared",
        "agent-stop" => "Agent turn stopped",
        "abom-session-end" => "Session ended",
        "abom-session-start" => "Session started",
        "tool-start" => "Tool started",
        "tool-complete" => "Tool completed",
        "custom-tool-start" => "Custom tool started",
        "custom-tool-complete" => "Custom tool completed",
        "mcp-server" => "MCP server",
        "session-start" => "Runtime session started",
        "session-end" => "Runtime session ended",
        "artifact" => "Artifact kept",
        _ => Words(hook)
    };
    public static string Purpose(string value) => value == "authoring" ? "Query authoring" : Words(value);
    public static string Timeout(SessionPolicyRecord policy) =>
        policy.TimeoutMs > 0 ? Span(TimeSpan.FromMilliseconds(policy.TimeoutMs)) : "Timeout not recorded";

    // The cap is Copilot's on what one session may spend. With none set, the plan's own limits apply.
    public static string Credits(SessionPolicyRecord policy) => !policy.CreditsKnown
        ? "AI credit cap not recorded"
        : policy.MaxAiCredits is { } cap ? $"Capped at {cap:g} AI credits" : "No AI credit cap";
    public const string NoCapNote = "Copilot's own plan limits still apply.";

    /// <summary>A length of time as a reader says it: 42 s, 8 min 1 s, 1 h 5 min.</summary>
    public static string Span(TimeSpan span)
    {
        if (span < TimeSpan.Zero) span = TimeSpan.Zero;
        if (span.TotalHours >= 1) return span.Minutes > 0 ? $"{(int)span.TotalHours} h {span.Minutes} min" : $"{(int)span.TotalHours} h";
        if (span.TotalMinutes >= 1) return span.Seconds > 0 ? $"{(int)span.TotalMinutes} min {span.Seconds} s" : $"{(int)span.TotalMinutes} min";
        return $"{span.Seconds} s";
    }

    /// <summary>Why a session is listed beside a run or a request, in the reader's terms.</summary>
    public static string Relation(AuditRelation relation) => relation.Kind switch
    {
        "worked request" => $"Worked request #{relation.RequestId}",
        "drafted from run" => $"Drafted a request from run #{relation.RunId}'s results",
        "request filed from run" => $"Request #{relation.RequestId} was filed from run #{relation.RunId}",
        "request filed using draft" => $"Request #{relation.RequestId} was filed from this session's draft",
        "observed version" => $"Worked on {relation.QueryId} at version {Short(relation.Version)}",
        "version evaluated by run" => $"Worked on {relation.QueryId} at the version run #{relation.RunId} evaluated",
        _ => Words(relation.Kind),
    };
    public static string Short(string version) => version.Length > 12 ? version[..12] : Value(version);
    public static string Value(string value) => value.Length > 0 ? value : "Not recorded";
    public static string Icon(string decision) => decision switch
    {
        "refused" or "denied" => Icons.Material.Outlined.Block,
        "failed" or "error" => Icons.Material.Outlined.ErrorOutline,
        "succeeded" or "finished" => Icons.Material.Outlined.CheckCircle,
        "allowed" => Icons.Material.Outlined.ArrowForward,
        "stopped" or "interrupted" => Icons.Material.Outlined.PauseCircle,
        _ => Icons.Material.Outlined.Info
    };
    public static string TestOutcome(AuditTestEvidence test) => test.Passed == true ? "Test passed"
        : test.Passed is null ? "Test outcome not recorded"
        : test.FailureStage.ToUpperInvariant() switch
        {
            "RESULT" => "Result comparison failed",
            "COMPILE" or "COMPILATION" => "Compilation failed",
            "EXTRACT" or "EXTRACTION" => "Extraction failed",
            _ => "Test failed"
        };
    public static string TestCounts(AuditTestEvidence test) =>
        test.Total is { } total && test.Failed is { } failed && total >= failed && failed >= 0
            ? $"{total - failed} of {total} passed"
            : "Comparison counts not recorded";
}
