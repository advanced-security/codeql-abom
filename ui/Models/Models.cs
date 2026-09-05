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
    public int AnalyzedUpstreams { get; init; }
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

/// <summary>Something the operator has to fix, with the exact way to fix it.</summary>
public sealed record DiscoveryProblem
{
    public string Reason { get; init; } = string.Empty;
    public string Remedy { get; init; } = string.Empty;
}

public sealed record DiscoveryStatus
{
    public string State { get; init; } = "stopped";
    public int Cycle { get; init; }
    public string CycleStart { get; init; } = string.Empty;
    public string LastCycleAt { get; init; } = string.Empty;
    public int Reclaimed { get; init; }
    public int Analyzed { get; init; }
    public int Settled { get; init; }
    public int Failed { get; init; }
    public int Unavailable { get; init; }
    public int Discovered { get; init; }
    public int Edges { get; init; }
    public int Rechecked { get; init; }
    public int Drifted { get; init; }

    /// <summary>Dependencies ingested since reach was last measured.</summary>
    public int ReachPending { get; init; }

    /// <summary>Arrivals that trigger an automatic recount, so ReachPending has a denominator.</summary>
    public int ReachThreshold { get; init; }
    public string LastRebuildReason { get; init; } = string.Empty;
    public long LastRebuildMillis { get; init; }
    public string LastError { get; init; } = string.Empty;
    public string LastErrorAt { get; init; } = string.Empty;
    public DiscoveryProblem? Problem { get; init; }

    /// <summary>What starting the loop will spend, so pressing Start is not an unpriced act.</summary>
    public int Concurrency { get; init; }
    public string WorkDir { get; init; } = string.Empty;
    public int ClaimsPerCycle { get; init; }

    /// <summary>Alive and claiming, or about to: an idle loop still picks up expiring retries.</summary>
    public bool IsRunning => State is "working" or "idle" or "pausing";

    /// <summary>Asked to stop, with work already in flight that will run to completion.</summary>
    public bool IsPausing => State == "pausing";
}

public sealed record GraphVersion
{
    public long Generation { get; init; }
    public int Nodes { get; init; }
    public int Edges { get; init; }
    public string UpdatedAt { get; init; } = string.Empty;

    /// <summary>When blast radius was last derived. Nodes added since carry no measured reach.</summary>
    public string ReachRecomputedAt { get; init; } = string.Empty;
}

public sealed record DiscoveryState
{
    public bool Available { get; init; }
    public DiscoveryStatus Status { get; init; } = new();
    public IReadOnlyDictionary<string, int> Frontier { get; init; } =
        new Dictionary<string, int>();

    /// <summary>Analyzed work per dependency tier, so re-analysis can state its cost.</summary>
    public IReadOnlyDictionary<string, int> Tiers { get; init; } =
        new Dictionary<string, int>();

    public GraphVersion Version { get; init; } = new();

    /// <summary>Empty when advisories were never collected.</summary>
    public string AdvisoriesFetchedAt { get; init; } = string.Empty;

    public int Pending => Frontier.TryGetValue("pending", out var n) ? n : 0;
    public int Running => Frontier.TryGetValue("running", out var n) ? n : 0;
    public int Done => Frontier.TryGetValue("done", out var n) ? n : 0;

    public int Count(string state) => Frontier.TryGetValue(state, out var n) ? n : 0;

    /// <summary>Everything not yet looked inside, however many attempts it has cost.</summary>
    public int Unresolved => Count("queued") + Count("retrying") + Running;
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
    public string ClaimedAt { get; init; } = string.Empty;
    public string NextAttemptAt { get; init; } = string.Empty;

    /// <summary>"runtime" when something in scope executes it, "build_time" when only its own project's CI does.</summary>
    public string SupplyChain { get; init; } = string.Empty;

    /// <summary>
    /// The state a reader distinguishes, which is one more than the queue stores: a dependency
    /// waiting on a retry has already cost something, and a queued one has not.
    /// </summary>
    public string State => Status switch
    {
        "pending" when Attempts > 0 => "retrying",
        "pending" => "queued",
        _ => Status,
    };
}

public sealed record ActionSummary
{
    public string NodeKey { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Owner { get; init; } = string.Empty;
    public string Repo { get; init; } = string.Empty;
    /// <summary>Separates what the organization owns from what it merely depends on.</summary>
    public string Origin { get; init; } = string.Empty;
    public int DependentRepos { get; init; }
    public int DirectDependents { get; init; }
    /// <summary>Different references pointing here; each is a separate upgrade decision.</summary>
    public int DistinctRefs { get; init; }
    public int Findings { get; init; }
    public int Advisories { get; init; }
    public bool Analyzed { get; init; }
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
    /// <summary>Marks a row in an account the operator belongs to. A mark, never a filter.</summary>
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

/// <summary>An account the operator is verified to belong to.</summary>
public sealed record OwnedAccount
{
    public string Owner { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Role { get; init; } = string.Empty;
}

/// <summary>A setup failure the operator can fix, and the command that fixes it.</summary>
public sealed record AccountProblem
{
    public string Reason { get; init; } = string.Empty;
    public string Remedy { get; init; } = string.Empty;
}

/// <summary>
/// Who gh is authenticated as and what that established.
///
/// A Problem is not the same as belonging to nothing: one is a broken setup, the other is an
/// answer, and they call for entirely different things from the reader.
/// </summary>
public sealed record AccountIdentity
{
    public bool Authenticated { get; init; }
    public string Login { get; init; } = string.Empty;
    public string Host { get; init; } = string.Empty;
    public DateTimeOffset? VerifiedAt { get; init; }
    public IReadOnlyList<OwnedAccount> Accounts { get; init; } = [];
    public AccountProblem? Problem { get; init; }
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
    public int Unanalyzed { get; init; }
}

/// <summary>What each dependency tier runs, and what it could run.</summary>
public sealed record SuiteBinding
{
    public IReadOnlyList<string> Available { get; init; } = [];
    public IReadOnlyDictionary<string, IReadOnlyList<string>> Tiers { get; init; } =
        new Dictionary<string, IReadOnlyList<string>>();
    /// <summary>Analyzed dependencies per tier, so the cost of a change is stated before it is spent.</summary>
    public IReadOnlyDictionary<string, int> Counts { get; init; } = new Dictionary<string, int>();
    /// <summary>What each suite last resolved to. A suite nobody resolved is absent, not zero.</summary>
    public IReadOnlyDictionary<string, SuiteDetail> Contents { get; init; } =
        new Dictionary<string, SuiteDetail>();
}

/// <summary>The packs behind a suite, which is where a version anyone can act on lives.</summary>
/// <remarks>A suite has no version of its own: it is a program over packs, not a list.</remarks>
public sealed record SuiteDetail
{
    public IReadOnlyList<PackDetail> Packs { get; init; } = [];
    public int QueryCount { get; init; }
    public string ResolvedAt { get; init; } = string.Empty;
}

public sealed record PackDetail
{
    public string Name { get; init; } = string.Empty;
    public string Version { get; init; } = string.Empty;
    public IReadOnlyList<string> Queries { get; init; } = [];
}

/// <summary>An <c>if:</c> on a route, and the workflow or action holding the step it guards.</summary>
/// <remarks>Verbatim. Whether it holds on a given run is the reader's judgement, not ABOM's.</remarks>
public sealed record StepCondition
{
    public string In { get; init; } = string.Empty;
    public string Condition { get; init; } = string.Empty;
}

/// <summary>One repository under review that reaches a node, and the route it takes.</summary>
/// <remarks>Named for the route because <c>NodeReach</c> already means an advisory's reach.</remarks>
public sealed record NodeRoute
{
    public string Repository { get; init; } = string.Empty;
    /// <summary>0 when the node lives here, 1 when this repository names its container itself.</summary>
    public int Hops { get; init; }
    public IReadOnlyList<string> Path { get; init; } = [];
    public IReadOnlyList<StepCondition> Conditions { get; init; } = [];
}

/// <summary>One reference and how widely it is used, verbatim and unjudged.</summary>
public sealed record RefUsage
{
    public string Ref { get; init; } = string.Empty;
    public int Count { get; init; }
    public int Repositories { get; init; }
}

public sealed record NodeNeighbor
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
    public bool Analyzed { get; init; }
    public int DependentRepos { get; init; }
    public int DirectDependents { get; init; }
    public IReadOnlyList<RefUsage> Refs { get; init; } = [];
    public IReadOnlyList<RuleCount> Rules { get; init; } = [];
    public IReadOnlyList<AdvisoryReach> Advisories { get; init; } = [];
    public IReadOnlyList<NodeNeighbor> UsedBy { get; init; } = [];
    public IReadOnlyList<NodeNeighbor> Uses { get; init; } = [];
}
