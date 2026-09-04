namespace Abom.WebAssembly.Models;

/// <summary>State of every stage, which is only meaningful read together.</summary>
public sealed record Summary
{
    public int SeedRepos { get; init; }
    public int UpstreamRepos { get; init; }
    public int Nodes { get; init; }
    public int Edges { get; init; }
    public int Findings { get; init; }
    public int Rules { get; init; }
    public int LinkedFindings { get; init; }
    public int Advisories { get; init; }
    public int AffectedActions { get; init; }
    public int Exposures { get; init; }
    public int DirectExposures { get; init; }
    public int AnalysedUpstreams { get; init; }
    public IReadOnlyDictionary<string, int> Frontier { get; init; } =
        new Dictionary<string, int>();

    public int PendingDependencies => Frontier.TryGetValue("pending", out var n) ? n : 0;
}

/// <summary>
/// One advisory ranked by published severity against how many repositories reach it.
/// CVSS and EPSS travel alongside but are deliberately absent from <see cref="Priority"/>.
/// </summary>
public sealed record RemediationItem
{
    public string GhsaId { get; init; } = string.Empty;
    public string CveId { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;
    public string Summary { get; init; } = string.Empty;
    public int DirectRepos { get; init; }
    public int TransitiveRepos { get; init; }
    public IReadOnlyList<string> Actions { get; init; } = [];
    public double CvssV3Score { get; init; }
    public bool HasCvss { get; init; }
    public double EpssPercentage { get; init; }
    public bool HasEpss { get; init; }
    public bool Fixable { get; init; }
    public int Priority { get; init; }

    public int TotalRepos => DirectRepos + TransitiveRepos;
}

public sealed record AffectedRange
{
    public string VulnerableRange { get; init; } = string.Empty;
    public string FirstPatched { get; init; } = string.Empty;
}

public sealed record NodeReach
{
    public string NodeKey { get; init; } = string.Empty;
    public int DependentRepos { get; init; }
    public IReadOnlyList<string> References { get; init; } = [];
}

public sealed record AdvisoryReach
{
    public string GhsaId { get; init; } = string.Empty;
    public string CveId { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;
    public string Summary { get; init; } = string.Empty;
    public string Url { get; init; } = string.Empty;
    public double CvssV3Score { get; init; }
    public bool HasCvss { get; init; }
    public double EpssPercentage { get; init; }
    public bool HasEpss { get; init; }
    public string PackageName { get; init; } = string.Empty;
    public IReadOnlyList<AffectedRange> Ranges { get; init; } = [];
    public IReadOnlyList<NodeReach> Nodes { get; init; } = [];
    public int MaxDependentRepos { get; init; }
}

/// <summary>One repository's route to an affected action. Hops of 1 is fixable in place.</summary>
public sealed record Exposure
{
    public string Repository { get; init; } = string.Empty;
    public string GhsaId { get; init; } = string.Empty;
    public string CveId { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;
    public string Summary { get; init; } = string.Empty;
    public string NodeKey { get; init; } = string.Empty;
    public int Hops { get; init; }
    public IReadOnlyList<string> Path { get; init; } = [];
    public IReadOnlyList<string> References { get; init; } = [];

    public bool IsDirect => Hops == 1;
}

public sealed record ExposureSummary
{
    public string GhsaId { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;
    public string Summary { get; init; } = string.Empty;
    public int Direct { get; init; }
    public int Transitive { get; init; }
}

public sealed record SubgraphNode
{
    public string Key { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;
    public string Origin { get; init; } = string.Empty;
    public bool IsExternal { get; init; }
    public string Path { get; init; } = string.Empty;
    public int Hops { get; init; }
    public int DependentRepos { get; init; }
    public int Findings { get; init; }
}

public sealed record SubgraphEdge
{
    public string Source { get; init; } = string.Empty;
    public string Target { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public IReadOnlyList<string> Refs { get; init; } = [];
    public int Count { get; init; }
}

public sealed record Subgraph
{
    public string Focus { get; init; } = string.Empty;
    public IReadOnlyList<SubgraphNode> Nodes { get; init; } = [];
    public IReadOnlyList<SubgraphEdge> Edges { get; init; } = [];
    public bool Truncated { get; init; }
}

public sealed record FrontierItem
{
    public string NodeKey { get; init; } = string.Empty;
    public string Owner { get; init; } = string.Empty;
    public string Repo { get; init; } = string.Empty;
    public string Status { get; init; } = string.Empty;
    public int Depth { get; init; }
    public double Score { get; init; }
    public int Attempts { get; init; }
    public int AnalysisCount { get; init; }
    public string ResolvedSha { get; init; } = string.Empty;
    public string DiscoveredFrom { get; init; } = string.Empty;
    public string Reason { get; init; } = string.Empty;
}

public sealed record ActionSummary
{
    public string NodeKey { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Owner { get; init; } = string.Empty;
    public string Repo { get; init; } = string.Empty;
    /// <summary>Separates what the organisation owns from what it merely depends on.</summary>
    public string Origin { get; init; } = string.Empty;
    public int DependentRepos { get; init; }
    public int DirectDependents { get; init; }
    /// <summary>Different references pointing here; each is a separate upgrade decision.</summary>
    public int DistinctRefs { get; init; }
    public int Findings { get; init; }
    public int Advisories { get; init; }
    public bool Analysed { get; init; }
    public IReadOnlyList<string> Refs { get; init; } = [];
}

public sealed record RepositorySummary
{
    public string FullName { get; init; } = string.Empty;
    public string Origin { get; init; } = string.Empty;
    public int Workflows { get; init; }
    public int Dependencies { get; init; }
    public int Findings { get; init; }
    public int Exposures { get; init; }
    public int DirectExposures { get; init; }
}

public sealed record RuleCount
{
    public string RuleId { get; init; } = string.Empty;
    public int Count { get; init; }
}

public sealed record WorkflowSummary
{
    public string NodeKey { get; init; } = string.Empty;
    public string Path { get; init; } = string.Empty;
    public int Dependencies { get; init; }
    public int Findings { get; init; }
}

public sealed record RepositoryDetail
{
    public string FullName { get; init; } = string.Empty;
    public string Origin { get; init; } = string.Empty;
    public IReadOnlyList<RuleCount> FindingsByRule { get; init; } = [];
    public IReadOnlyList<WorkflowSummary> Workflows { get; init; } = [];
    public IReadOnlyList<Exposure> Exposures { get; init; } = [];
}

/// <summary>
/// One scope's reference pinning, measured two independent ways: how references are written,
/// and what <c>actions/unpinned-tag</c> objected to. They are never combined.
/// </summary>
/// <summary>The filter every pinning level is computed from, so parents match their children.</summary>
public sealed record PinningQuery
{
    public string Search { get; init; } = string.Empty;
    /// <summary>`commit`, `tag`, or empty. Lexical: how the reference is written.</summary>
    public string RefStyle { get; init; } = string.Empty;
    public bool FlaggedOnly { get; init; }

    public bool IsActive =>
        !string.IsNullOrWhiteSpace(Search) || !string.IsNullOrEmpty(RefStyle) || FlaggedOnly;

    public string Query =>
        $"&q={Uri.EscapeDataString(Search)}&style={Uri.EscapeDataString(RefStyle)}" +
        $"&flagged={(FlaggedOnly ? "true" : "false")}";
}

public sealed record PinningRow
{
    public string Scope { get; init; } = string.Empty;
    public string ScopeKey { get; init; } = string.Empty;
    public string Label { get; init; } = string.Empty;
    public string Ref { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public int Pinnable { get; init; }
    public int Commit { get; init; }
    public int Floating { get; init; }
    public int Flagged { get; init; }
    public int FloatingUnflagged { get; init; }
    public bool HasChildren { get; init; }
    /// <summary>Marks a row in an account the operator declared. A mark, never a filter.</summary>
    public bool Owned { get; init; }
    public OwnedSplit Yours { get; init; } = new();
    public OwnedSplit FlaggedSplit { get; init; } = new();

    public double CommitShare => Pinnable == 0 ? 0 : (double)Commit / Pinnable;
}

/// <summary>A figure divided into what the operator can act on and what they cannot.</summary>
public sealed record OwnedSplit
{
    public int Yours { get; init; }
    public int Elsewhere { get; init; }
    public int Total => Yours + Elsewhere;
}

public sealed record OwnerCandidate
{
    public string Owner { get; init; } = string.Empty;
    public int Repositories { get; init; }
    public int Findings { get; init; }
    public bool Owned { get; init; }
}

public sealed record AffectedRepo
{
    public string FullName { get; init; } = string.Empty;
    public string Origin { get; init; } = string.Empty;
    public int Hops { get; init; }
    public IReadOnlyList<string> Refs { get; init; } = [];
}

/// <summary>Who would be affected by changing a node's version.</summary>
public sealed record BumpImpact
{
    public string Key { get; init; } = string.Empty;
    public int Direct { get; init; }
    public int Transitive { get; init; }
    public IReadOnlyList<AffectedRepo> Repos { get; init; } = [];
    public int Unanalysed { get; init; }
}

/// <summary>One reference and how widely it is used, verbatim and unjudged.</summary>
public sealed record RefUsage
{
    public string Ref { get; init; } = string.Empty;
    public int Count { get; init; }
    public int Repositories { get; init; }
}

public sealed record NodeNeighbour
{
    public string Key { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;
    public string Origin { get; init; } = string.Empty;
    public IReadOnlyList<string> Refs { get; init; } = [];
    public int Count { get; init; }
}

/// <summary>Everything known about one node, as its own page shows it.</summary>
public sealed record NodeDetail
{
    public string Key { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;
    public string Origin { get; init; } = string.Empty;
    public string Path { get; init; } = string.Empty;
    public bool IsExternal { get; init; }
    public bool Analysed { get; init; }
    public int DependentRepos { get; init; }
    public int DirectDependents { get; init; }
    public IReadOnlyList<RefUsage> Refs { get; init; } = [];
    public IReadOnlyList<RuleCount> Rules { get; init; } = [];
    public IReadOnlyList<AdvisoryReach> Advisories { get; init; } = [];
    public IReadOnlyList<NodeNeighbour> UsedBy { get; init; } = [];
    public IReadOnlyList<NodeNeighbour> Uses { get; init; } = [];
}
