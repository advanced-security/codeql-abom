namespace Abom.WebAssembly.Models;

/// <summary>State of every stage, read as one snapshot.</summary>
public sealed record Summary
{
    public int SeedRepos { get; init; }

    /// <summary>Seeded repositories claimed by the declaration.</summary>
    public int OwnedRepos { get; init; }
    public int UpstreamRepos { get; init; }
    public int Nodes { get; init; }

    /// <summary>Nodes that something depends on; excludes workflow structure nodes.</summary>
    public int Dependencies { get; init; }
    public int Edges { get; init; }
    public int Findings { get; init; }

    /// <summary>Findings in repositories claimed by the declaration.</summary>
    public int OwnedFindings { get; init; }

    /// <summary>The same findings by band of their rules' own scores.</summary>
    public SeverityCounts OwnedSeverity { get; init; } = new();
    public int Rules { get; init; }
    public int LinkedFindings { get; init; }
    public int Advisories { get; init; }
    public int AffectedActions { get; init; }
    public int Exposures { get; init; }
    public int DirectExposures { get; init; }

    /// <summary>Routes that start in a repository the declaration claims.</summary>
    public int OwnedExposures { get; init; }
    public int OwnedDirectExposures { get; init; }
    public int AnalyzedUpstreams { get; init; }
    public IReadOnlyDictionary<string, int> Frontier { get; init; } =
        new Dictionary<string, int>();

    public int PendingDependencies => Frontier.TryGetValue("pending", out var n) ? n : 0;
}

/// <summary>Finding counts by rule score band; unknown is not low severity.</summary>
public sealed record SeverityCounts
{
    public int Critical { get; init; }
    public int High { get; init; }
    public int Medium { get; init; }
    public int Low { get; init; }
    public int Unknown { get; init; }

    /// <summary>The most severe band holding a finding, empty when none does.</summary>
    public string Worst =>
        Critical > 0 ? "critical" : High > 0 ? "high" : Medium > 0 ? "medium" :
        Low > 0 ? "low" : Unknown > 0 ? "unknown" : string.Empty;
}

/// <summary>Advisory ranked by severity and reachable repository count.</summary>
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
    public int Rank { get; init; }

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

    /// <summary>Repositories already recorded by the ledger.</summary>
    public int Skipped { get; init; }

    /// <summary>Checkouts served from the cache instead of cloned.</summary>
    public int Reused { get; init; }
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

/// <summary>Selective requeue result plus the state it left behind.</summary>
public sealed record RequeueResult
{
    public int Requeued { get; init; }
    public int Expedited { get; init; }
    public string CliVersion { get; init; } = string.Empty;
    public int Packs { get; init; }
    public int NamesNoRepository { get; init; }
    public DiscoveryState State { get; init; } = new();
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

    /// <summary>Each tier's size and how much of it is analyzed.</summary>
    public IReadOnlyDictionary<string, TierCount> Tiers { get; init; } =
        new Dictionary<string, TierCount>();

    public GraphVersion Version { get; init; } = new();

    /// <summary>Whether server start may resume discovery by recorded intent.</summary>
    public bool ResumeOnStart { get; init; }

    /// <summary>Whether discovery has ever started in this installation.</summary>
    public bool EverStarted { get; init; }

    /// <summary>Empty when advisories were never collected.</summary>
    public string AdvisoriesFetchedAt { get; init; } = string.Empty;

    public int Pending => Frontier.TryGetValue("pending", out var n) ? n : 0;
    public int Running => Frontier.TryGetValue("running", out var n) ? n : 0;
    public int Done => Frontier.TryGetValue("done", out var n) ? n : 0;

    public int Count(string state) => Frontier.TryGetValue(state, out var n) ? n : 0;

    /// <summary>Queued, retrying, or running dependencies.</summary>
    public int Unresolved => Count("queued") + Count("retrying") + Running;
}

public sealed record FrontierItem
{
    public string NodeKey { get; init; } = string.Empty;

    /// <summary>The analyzed reference, empty for the default branch.</summary>
    public string Ref { get; init; } = string.Empty;

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

    /// <summary>Empty when nothing has ever analyzed this.</summary>
    public string AnalyzedAt { get; init; } = string.Empty;

    /// <summary>Which tier this sits in, and therefore which suites analyzed it.</summary>
    public string Tier { get; init; } = string.Empty;

    /// <summary>"runtime" when an analyzed repository executes it, "build_time" when only its own project's CI does.</summary>
    public string SupplyChain { get; init; } = string.Empty;

    /// <summary>Suites that analyzed this row, formatted as <c>pack@version</c>.</summary>
    public IReadOnlyList<string> Suites { get; init; } = Array.Empty<string>();

    /// <summary>False when suite identity was not recorded.</summary>
    public bool SuitesKnown { get; init; }

    /// <summary>The CodeQL that produced the row, empty when unrecorded.</summary>
    public string CliVersion { get; init; } = string.Empty;

    /// <summary>Bounded preview of repositories running this exact version.</summary>
    public IReadOnlyList<string> UsedBy { get; init; } = Array.Empty<string>();

    /// <summary>How many repositories name this version in total.</summary>
    public int UsedByCount { get; init; }

    /// <summary>How many listed users are in repositories the operator can change.</summary>
    public int UsedByOwned { get; init; }

    /// <summary>Bounded preview of repositories whose lock selects this commit.</summary>
    public IReadOnlyList<string> LockedBy { get; init; } = Array.Empty<string>();

    /// <summary>How many repositories' locks select this commit in total.</summary>
    public int LockedByCount { get; init; }

    /// <summary>UI state that distinguishes fresh queued work from retries.</summary>
    public string State => Status switch
    {
        "pending" when Attempts > 0 => "retrying",
        "pending" => "queued",
        _ => Status,
    };
}

/// <summary>Filter term from the server catalog, plus control metadata.</summary>
public sealed record FilterTerm
{
    public string Name { get; init; } = string.Empty;
    public string Label { get; init; } = string.Empty;
    public string Help { get; init; } = string.Empty;

    /// <summary>How the value is written: scalar, list, property, number or flag.</summary>
    public string Arity { get; init; } = string.Empty;

    /// <summary>The closed list this term allows. Empty means its values are not the language's.</summary>
    public IReadOnlyList<string> Values { get; init; } = [];

    /// <summary>Where to read values that come from the corpus. Empty means the value is typed.</summary>
    public string ValuesFrom { get; init; } = string.Empty;

    /// <summary>The surfaces that apply this term. Elsewhere it would be refused by name.</summary>
    public IReadOnlyList<string> Surfaces { get; init; } = [];

    /// <summary>The values a surface answers, where it answers fewer than the term allows.</summary>
    public IReadOnlyDictionary<string, IReadOnlyList<string>> SurfaceValues { get; init; } =
        new Dictionary<string, IReadOnlyList<string>>();
}

/// <summary>
/// One value a filter term can take, and how much of the corpus takes it.
/// </summary>
///
/// <remarks>
/// The count is zero for a value the language declares rather than the corpus supplies. Zero there
/// means nobody counted, not that nothing matches, which is why it is not rendered.
/// </remarks>
public sealed record FilterValue
{
    public string Value { get; init; } = string.Empty;
    public int Count { get; init; }
}

/// <summary>
/// What a term can take, and how many rows recorded no value at all.
/// </summary>
///
/// <remarks>
/// The two travel together because a value list is built from rows that recorded one: a column
/// most rows left empty produces a short, confident list describing a fraction of the population.
/// Zero uncovered is a measurement — the list is everything — rather than a missing number.
/// </remarks>
public sealed record FilterValueSet
{
    public IReadOnlyList<FilterValue> Values { get; init; } = [];
    public int Uncovered { get; init; }
}

/// <summary>A question worth asking repeatedly, named. A tab is one of these, not a page.</summary>
///
/// <remarks>
/// A built-in ships with the product; a saved one is an operator's own. <c>Builtin</c> is what
/// tells a reader which they are looking at, and the two sit side by side deliberately.
/// </remarks>
public sealed record NamedView
{
    public string Name { get; init; } = string.Empty;
    public string Title { get; init; } = string.Empty;
    public string Predicate { get; init; } = string.Empty;

    /// <summary>
    /// How the Explorer drew it, empty when whoever saved it had nothing to draw. Empty means
    /// "however you would anyway", never "with every default".
    /// </summary>
    public string Presentation { get; init; } = string.Empty;

    public bool Builtin { get; init; }
    public string CreatedBy { get; init; } = string.Empty;

    /// <summary>
    /// For every page a scope opens on, the terms that page cannot apply — empty where it applies
    /// all of them. A page missing from it is one the server said nothing about, never one that
    /// applies everything.
    /// </summary>
    public IReadOnlyDictionary<string, IReadOnlyList<string>> Refuses { get; init; } =
        new Dictionary<string, IReadOnlyList<string>>();

    /// <summary>What to show on the tab. A saved view has only the name its author gave it.</summary>
    public string Label => Title.Length > 0 ? Title : Name;
}

/// <summary>One workflow as its unit lists it.</summary>
///
/// None of these measure being depended upon — a workflow is a consumer, which is why it has a unit
/// rather than a row in the actions grid.
public sealed record WorkflowRow
{
    public string NodeKey { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;
    public string Path { get; init; } = string.Empty;
    public string Triggers { get; init; } = string.Empty;
    public int Jobs { get; init; }
    public int Steps { get; init; }
    public int Dependencies { get; init; }
    public int PrivilegedJobs { get; init; }
    public int Findings { get; init; }
    public SeverityCounts Severity { get; init; } = new();
    public int DependedOnBy { get; init; }
}

public sealed record WorkflowsPage
{
    public IReadOnlyList<WorkflowRow> Items { get; init; } = [];
    public int Total { get; init; }
}

/// <summary>One job, with the facts the extraction recorded against it rather than aggregated.</summary>
public sealed record JobRow
{
    public string NodeKey { get; init; } = string.Empty;
    public string JobId { get; init; } = string.Empty;
    public string Workflow { get; init; } = string.Empty;
    public string RunsOn { get; init; } = string.Empty;
    public string PermissionsScope { get; init; } = string.Empty;
    public bool Privileged { get; init; }
    public bool ExternallyTriggerable { get; init; }
    public bool SecretsInherit { get; init; }
    public int Steps { get; init; }
    public int Dependencies { get; init; }
    public int Findings { get; init; }
    public SeverityCounts Severity { get; init; } = new();
}

public sealed record JobsPage
{
    public IReadOnlyList<JobRow> Items { get; init; } = [];
    public int Total { get; init; }
}

public sealed record FindingReview
{
    public long Id { get; init; }
    public string RuleId { get; init; } = string.Empty;
    public string Title { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;
    public string FilePath { get; init; } = string.Empty;
    public int StartLine { get; init; }
    public string Message { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;
    public double? SecuritySeverity { get; init; }
    public string Provider { get; init; } = string.Empty;
    public long QueryRunId { get; init; }
    public string Commit { get; init; } = string.Empty;
    public string RecordedAt { get; init; } = string.Empty;
    public bool Linked { get; init; }
    public bool HelpAvailable { get; init; }
    public bool EvidenceAvailable { get; init; }
    public string DisplayTitle => Title.Length > 0 ? Title : RuleId;
    public string Location => FilePath.Length == 0 ? "Location not recorded"
        : StartLine > 0 ? $"{FilePath}:{StartLine}" : FilePath;
    public string Band => SecuritySeverity switch
    {
        >= 9 => "critical", >= 7 => "high", >= 4 => "medium", > 0 => "low", _ => "unknown",
    };
}

public sealed record FindingsPage
{
    public IReadOnlyList<FindingReview> Items { get; init; } = [];
    public int Total { get; init; }
    public int UnlinkedExcluded { get; init; }
}

public sealed record FindingDetails
{
    public FindingReview Finding { get; init; } = new();
    public IReadOnlyList<FindingEvaluation> Evaluations { get; init; } = [];
    public bool EvaluationsTruncated { get; init; }
    public long SelectedEvaluationId { get; init; }
    public QueryDocumentation? Documentation { get; init; }
    public ReviewResult? Result { get; init; }
    public IReadOnlyList<FindingAssetLink> Assets { get; init; } = [];
    public IReadOnlyList<string> References { get; init; } = [];
}

public sealed record QueryDocumentation
{
    public string Title { get; init; } = string.Empty;
    public string Description { get; init; } = string.Empty;
    public string HelpMarkdown { get; init; } = string.Empty;
    public string HelpText { get; init; } = string.Empty;
    public string HelpUri { get; init; } = string.Empty;
    public IReadOnlyList<string>? Tags { get; init; }
}

public sealed record FindingAssetLink
{
    public string Key { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
}

public sealed record ReviewResult
{
    public ReviewMessage? Message { get; init; }
    public IReadOnlyList<ReviewLocation>? Locations { get; init; }
    public IReadOnlyList<ReviewLocation>? RelatedLocations { get; init; }
    public IReadOnlyList<ReviewCodeFlow>? CodeFlows { get; init; }
}

public sealed record ReviewMessage
{
    public string Text { get; init; } = string.Empty;
    public string Markdown { get; init; } = string.Empty;
}

public sealed record ReviewLocation
{
    public int Id { get; init; }
    public ReviewMessage? Message { get; init; }
    public ReviewPhysicalLocation? PhysicalLocation { get; init; }
    public string Path => PhysicalLocation?.ArtifactLocation?.Uri ?? string.Empty;
    public int Line => PhysicalLocation?.Region?.StartLine ?? 0;
    public string Label => Path.Length == 0 ? "Location not recorded" : Line > 0 ? $"{Path}:{Line}" : Path;
    public string Excerpt => PhysicalLocation?.ContextRegion?.Snippet?.Text
        ?? PhysicalLocation?.Region?.Snippet?.Text ?? string.Empty;
}

public sealed record ReviewPhysicalLocation
{
    public ReviewArtifactLocation? ArtifactLocation { get; init; }
    public ReviewRegion? Region { get; init; }
    public ReviewRegion? ContextRegion { get; init; }
}

public sealed record ReviewArtifactLocation
{
    public string Uri { get; init; } = string.Empty;
}

public sealed record ReviewRegion
{
    public int StartLine { get; init; }
    public int EndLine { get; init; }
    public int StartColumn { get; init; }
    public int EndColumn { get; init; }
    public ReviewMessage? Snippet { get; init; }
}

public sealed record ReviewCodeFlow
{
    public ReviewMessage? Message { get; init; }
    public IReadOnlyList<ReviewThreadFlow>? ThreadFlows { get; init; }
}

public sealed record ReviewThreadFlow
{
    public IReadOnlyList<ReviewThreadLocation>? Locations { get; init; }
}

public sealed record ReviewThreadLocation
{
    public ReviewLocation? Location { get; init; }
}

/// <summary>One page of a listing, and the size of the match it was cut from.</summary>
///
/// The total is not decoration: a page that does not carry it reads as the whole answer, and a
/// column sorted within it answers a narrower question than its header claims.
public sealed record InventoryPage
{
    public IReadOnlyList<ActionSummary> Items { get; init; } = [];
    public int Total { get; init; }

    /// <summary>Kinds outside this view that the findings terms also matched.</summary>
    ///
    /// Without it a findings question answers 0 and the zero reads as "nothing is wrong": every
    /// `actions/unpinned-tag` finding sits on a step or job node, which are not dependency kinds.
    public IReadOnlyList<KindCount> Elsewhere { get; init; } = [];

    /// <summary>
    /// Rows carrying findings the question matches with no recorded source, counted only when a
    /// source is asked: those are the rows the source term could not answer for.
    /// </summary>
    public int Unsourced { get; init; }

    /// <summary>The same for a provider term: rows whose matching finding recorded no provider.</summary>
    public int Unprovided { get; init; }
}

/// <summary>How many nodes of one kind a predicate matched.</summary>
public sealed record KindCount
{
    public string Kind { get; init; } = string.Empty;
    public int Count { get; init; }
}

/// <summary>One page of advisories, the size of the match, and what the match excludes.</summary>
public sealed record AdvisoriesPage
{
    public IReadOnlyList<RemediationItem> Items { get; init; } = [];
    public int Total { get; init; }

    /// <summary>
    /// Live advisories that name no action anything under review uses.
    /// </summary>
    ///
    /// The exclusion is correct and was invisible: a strip counting 11 over a table holding 3 told
    /// a reader eight had gone somewhere without saying where, and one of them was critical.
    public int Unreached { get; init; }
}

/// <summary>One declared relationship: a `uses:` or `needs:` line, as a row.</summary>
///
/// The facts are nullable because each is tri-state. A relationship whose route was never walked
/// is not a relationship on no route, and rendering `false` for both would make "nothing here
/// reaches an advisory" look the same as "nobody has looked".
public sealed record RelationshipSummary
{
    /// <summary>The source location that declares it, which is what a selection names.</summary>
    public string EdgeKey { get; init; } = "";
    public string Kind { get; init; } = "";
    public string Source { get; init; } = "";
    public string Target { get; init; } = "";
    public string Ref { get; init; } = "";
    public string RefStyle { get; init; } = "";
    public string Boundary { get; init; } = "";
    public string Repository { get; init; } = "";
    public string FilePath { get; init; } = "";
    public int StartLine { get; init; }

    /// <summary>What was reported against the line itself, rather than against either end.</summary>
    public int Findings { get; init; }
    public SeverityCounts Severity { get; init; } = new();
    public IReadOnlyList<string> Rules { get; init; } = [];

    public bool? AdvisoryRoute { get; init; }
    public bool? FindingRoute { get; init; }
    public int? OwnedDepth { get; init; }

    /// <summary>
    /// The commits current locks select for this line: the repository's own lock through the
    /// workflow named, or a caller's through the pin it reached the composite by.
    /// </summary>
    public IReadOnlyList<RelationshipLock> Locked { get; init; } = [];
}

/// <summary>One commit a current lock selects for one declaring line.</summary>
public sealed record RelationshipLock
{
    public string Commit { get; init; } = "";
    public string Consumer { get; init; } = "";
    public string Workflow { get; init; } = "";
    public string Via { get; init; } = "";
}

/// <summary>One page of relationships, and the size of the match it was cut from.</summary>
public sealed record RelationshipsPage
{
    public IReadOnlyList<RelationshipSummary> Items { get; init; } = [];
    public int Total { get; init; }
}

/// <summary>One page of repositories, and the size of the match it was cut from.</summary>
public sealed record RepositoriesPage
{
    public IReadOnlyList<RepositorySummary> Items { get; init; } = [];
    public int Total { get; init; }
}

/// <summary>A refusal the server states deliberately, carried back by name.</summary>
public sealed record ApiError
{
    public string Error { get; init; } = string.Empty;
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
    public SeverityCounts Severity { get; init; } = new();
    public int Advisories { get; init; }
    public bool Analyzed { get; init; }
    public IReadOnlyList<string> Refs { get; init; } = [];
}

public sealed record RepositoryProperty
{
    public string Name { get; init; } = string.Empty;
    public string Value { get; init; } = string.Empty;
}

public sealed record RepositorySummary
{
    public string FullName { get; init; } = string.Empty;
    public string Origin { get; init; } = string.Empty;
    public int Workflows { get; init; }
    public int Dependencies { get; init; }
    public int Findings { get; init; }
    public SeverityCounts Severity { get; init; } = new();
    public IReadOnlyList<RepositoryProperty> Properties { get; init; } = [];
    public int Exposures { get; init; }
    public int DirectExposures { get; init; }
}

/// <summary>
/// What a bill of materials claims about itself, counted by the server from the document a
/// download would carry.
/// </summary>
public sealed record BomSummary
{
    public IReadOnlyList<string> Roots { get; init; } = [];
    public string GeneratedAt { get; init; } = string.Empty;
    public BomCoverage Coverage { get; init; } = new();
    public int Direct { get; init; }
    public int Indirect { get; init; }
    public int DeepestHop { get; init; }
    public int WithFindings { get; init; }
    public int NoneFound { get; init; }
    public int NotExamined { get; init; }
}

public sealed record BomCoverage
{
    public int ComponentsInUse { get; init; }
    public int ComponentsAnalyzed { get; init; }
    public int ReferencesInUse { get; init; }
    public int ReferencesAnalyzed { get; init; }
    public int Findings { get; init; }
    public int FindingsUntraced { get; init; }
    public int FindingsWithoutHash { get; init; }
    public int FindingsWithoutDigest { get; init; }
}

public sealed record RuleCount
{
    public string RuleId { get; init; } = string.Empty;
    public int Count { get; init; }

    /// <summary>Where the queries that reported these findings came from, as their evaluations recorded.</summary>
    public IReadOnlyList<string> Sources { get; init; } = [];

    /// <summary>Findings no evaluation recorded a source for, which are not known to be from any.</summary>
    public int Unsourced { get; init; }

    /// <summary>The same findings by band; one rule usually lands in one.</summary>
    public SeverityCounts Severity { get; init; } = new();
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

    /// <summary>
    /// Whether the role carries enough access to answer for the account. Decided by the server
    /// from the role, never re-derived here: the interface reading `role` and reaching its own
    /// conclusion is the second derivation that lets the two disagree.
    /// </summary>
    public bool CanDeclare { get; init; }
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

    /// <summary>
    /// Whether the token could see the whole organization list. False means a declaration absent
    /// from it proves nothing, so asking gh again cannot settle anything and the remedy is a
    /// differently scoped token. Decided by the server, never re-derived from the scope strings.
    /// </summary>
    public bool CanReadMemberships { get; init; }
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

/// <summary>A tier's size and how much of it is currently analyzed.</summary>
public sealed record TierCount
{
    /// <summary>Every queue row in the tier, whatever state it is in.</summary>
    public int Total { get; init; }

    /// <summary>How many are analyzed now. A requeue empties this and leaves Total alone.</summary>
    public int Analyzed { get; init; }

    /// <summary>Rows standing for the operator's own repositories, which enumeration queues at
    /// depth 0. They joined the owned tier when enumeration started, and one number for both hid
    /// that all 91 analyzed rows were repositories and none of the 44 owned dependencies had been
    /// looked at.</summary>
    public int Repositories { get; init; }

    public int RepositoriesAnalyzed { get; init; }

    /// <summary>The tier's work that is somebody's dependency rather than the operator's own
    /// repository. Derived, so the halves cannot disagree with the total.</summary>
    public int Dependencies => Total - Repositories;

    public int DependenciesAnalyzed => Analyzed - RepositoriesAnalyzed;

    /// <summary>True when the tier holds both kinds and reporting one number would conflate
    /// them. Only the owned tier ever can.</summary>
    public bool HoldsRepositories => Repositories > 0;
}

/// <summary>What each dependency tier runs, and what it could run.</summary>
public sealed record SuiteBinding
{
    public IReadOnlyList<string> Available { get; init; } = [];
    public IReadOnlyDictionary<string, IReadOnlyList<string>> Tiers { get; init; } =
        new Dictionary<string, IReadOnlyList<string>>();

    /// <summary>Each tier's size and analyzed count, so a change states its cost before it is spent.</summary>
    public IReadOnlyDictionary<string, TierCount> Counts { get; init; } =
        new Dictionary<string, TierCount>();

    /// <summary>What each suite last resolved to. A suite nobody resolved is absent, not zero.</summary>
    public IReadOnlyDictionary<string, SuiteDetail> Contents { get; init; } =
        new Dictionary<string, SuiteDetail>();
    /// <summary>Whether an identity has been established. Without one an owned count of zero is a
    /// claim rather than a measurement, so the tier is not offered at all.</summary>
    public bool Verified { get; init; }
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

/// <summary>One repository that reaches a node, and the route it takes.</summary>
/// <remarks>Named for the route because <c>NodeReach</c> already means an advisory's reach.</remarks>
public sealed record NodeRoute
{
    public string Repository { get; init; } = string.Empty;
    /// <summary>0 when the node lives here, 1 when this repository names its container itself.</summary>
    public int Hops { get; init; }
    public IReadOnlyList<string> Path { get; init; } = [];
    public IReadOnlyList<StepCondition> Conditions { get; init; } = [];
}

/// <summary>
/// One account the operator has stated is theirs — the fourth ownership notion, and the only one
/// that means responsibility.
///
/// <c>repository.origin</c> and <see cref="OwnedAccount"/> are evidence, not responsibility.
/// </summary>
public sealed record DeclaredAccount
{
    public string Owner { get; init; } = string.Empty;

    /// <summary><c>organization</c> or <c>user</c>. A user account is opted into.</summary>
    public string Kind { get; init; } = string.Empty;

    public bool Enabled { get; init; }

    /// <summary>True when the account is restricted to <see cref="Includes"/>.</summary>
    public bool IncludesOnly { get; init; }

    public IReadOnlyList<string> Includes { get; init; } = [];
    public string DeclaredAt { get; init; } = string.Empty;
    public EnumerationMarks? Enumeration { get; init; }

    /// <summary>
    /// How the row arrived: <c>cli</c>, <c>interface</c>, or empty when it predates the column and
    /// nobody can account for it.
    /// </summary>
    public string DeclaredBy { get; init; } = string.Empty;

    /// <summary>
    /// What <c>gh</c> has to say: <c>confirmed</c>, <c>unknown</c> (it has not answered) or
    /// <c>contradicted</c> (it answered and this account was not among them). Decided by the
    /// server, never re-derived here.
    /// </summary>
    public string State { get; init; } = string.Empty;

    /// <summary>True when gh answered and did not list this account.</summary>
    public bool Contradicted => State == "contradicted";

    /// <summary>True when nobody can say how this declaration got here.</summary>
    public bool Unaccounted => string.IsNullOrEmpty(DeclaredBy);
}

public sealed record EnumerationMarks
{
    public string Route { get; init; } = string.Empty;
    public string ListedAt { get; init; } = string.Empty;
    public string SweptAt { get; init; } = string.Empty;
    public string HighCreated { get; init; } = string.Empty;
    public string HighPushed { get; init; } = string.Empty;

    /// <summary>
    /// Declared repositories a full listing of the account did not name, which are no longer the
    /// operator's. Empty says nothing is missing only where <see cref="ListedAt"/> is set.
    /// </summary>
    public IReadOnlyList<RepositoryAbsence> Missing { get; init; } = [];
}

/// <summary>A declared repository a full listing did not name, and since when.</summary>
public sealed record RepositoryAbsence
{
    public string Repository { get; init; } = string.Empty;
    public string MissingAt { get; init; } = string.Empty;
}

/// <summary>
/// What a candidate declaration would admit and drop, answered before it is written.
/// </summary>
/// <remarks>
/// Repositories are named rather than only counted: recognizing one is how an operator catches a
/// declaration they did not mean to make, and "417 repositories leave" is a number nobody can
/// check.
/// </remarks>
public sealed record ScopePreview
{
    /// <summary>Repositories the declaration names directly. The closure is what they reach, so
    /// the two differ by the whole supply chain.</summary>
    public int RootsNow { get; init; }
    public int RootsNext { get; init; }
    public int ClosureNow { get; init; }
    public int ClosureNext { get; init; }

    public IReadOnlyList<string> Entering { get; init; } = [];
    public IReadOnlyList<string> Leaving { get; init; } = [];
    public int EnteringCount { get; init; }
    public int LeavingCount { get; init; }
    public bool Truncated { get; init; }

    /// <summary>Queue rows inside the closure. A scope can shrink without any queued work
    /// stopping, which a repository count alone cannot show.</summary>
    public int QueuedNow { get; init; }
    public int QueuedNext { get; init; }

    /// <summary>True when the candidate would reach no repository the corpus holds. Declaring
    /// such an account is legitimate; silently succeeding is what leaves every page empty.</summary>
    public bool ReachesNothing => ClosureNext == 0;
}

/// <summary>What declaring an account turned out to mean.</summary>
/// <remarks>Declaring names accounts; enumerating turns them into repositories.</remarks>
public sealed record EnumerationReport
{
    /// <summary>Repositories the corpus had never seen.</summary>
    public int Added { get; init; }

    /// <summary>Repositories it held only as somebody else's dependency and now holds as the
    /// operator's own. Kept apart from <see cref="Added"/> because a run that is all promotions
    /// found nothing new, and one that is all additions was not crawling their work at all.</summary>
    public int Promoted { get; init; }

    /// <summary>Repositories already recorded as theirs, which is what a second run should be
    /// almost entirely made of.</summary>
    public int Unchanged { get; init; }

    public IReadOnlyList<AccountEnumeration> Accounts { get; init; } = [];

    /// <summary>Accounts that could not be reached. Named rather than counted, so that a short
    /// report reads as an incomplete answer rather than a small estate.</summary>
    public IReadOnlyList<AccountFailure> Failed { get; init; } = [];

    /// <summary>Repositories put in the analysis queue. Recording a repository is not analyzing
    /// it, and a corpus of rows nothing will ever read looks the same as an empty one.</summary>
    public int Queued { get; init; }

    /// <summary>Set when the repositories were recorded and could not be queued.</summary>
    public string? QueueError { get; init; }

    /// <summary>Repositories seen across every account that answered.</summary>
    public int Found => Accounts.Sum(a => a.Found);

    /// <summary>True when nothing was learned and nothing failed — a second run over an unchanged
    /// estate, which is not worth reporting as an event.</summary>
    public bool Quiet => Added == 0 && Promoted == 0 && Failed.Count == 0;
}

/// <summary>One declared account's contribution to an enumeration.</summary>
public sealed record AccountEnumeration
{
    public string Owner { get; init; } = "";
    public int Found { get; init; }
    public int Added { get; init; }
    public int Promoted { get; init; }
}

/// <summary>An account that could not be enumerated, with the reason.</summary>
public sealed record AccountFailure
{
    public string Owner { get; init; } = "";
    public string Error { get; init; } = "";
}

/// <summary>What removal would cost, or what it just cost.</summary>
/// <remarks>
/// The declared and reachable counts sit beside the removal because they are what decides it: a
/// corpus looks enormous until you see that one declared repository reaches 270 of its 1,099.
/// </remarks>
public sealed record PruneView
{
    public int Declared { get; init; }
    public int Reachable { get; init; }
    public PruneRemoval Removed { get; init; } = new();

    /// <summary>What is already out of view and still on disk. Zero means "delete permanently"
    /// would do nothing.</summary>
    public int Tombstoned { get; init; }

    /// <summary>False for an estimate, true for an outcome. One shape answering both would let
    /// the page report what it would do as what it did.</summary>
    public bool Applied { get; init; }

    /// <summary>Why no removal could be measured, empty when one could. Distinct from a zero
    /// report, which says nothing is outside the closure rather than that nothing is known.</summary>
    public string Unavailable { get; init; } = string.Empty;

    public bool RemovesNothing => Removed.Nodes == 0 && Removed.Edges == 0 && Removed.Queued == 0;
}

/// <summary>The last answer to what no longer belongs, and when it was asked.</summary>
/// <remarks>
/// Written by a check that runs at startup off the request path, so the interface can raise what
/// it found without anybody going looking for it.
/// </remarks>
public sealed record Staleness
{
    /// <summary>
    /// False until the first check finishes. "Nothing has looked" and "nothing needs removing"
    /// are different answers, and offering the second before a check has run would tell an
    /// operator their corpus is tidy on no evidence.
    /// </summary>
    public bool Checked { get; init; }
    public string CheckedAt { get; init; } = string.Empty;
    public PruneView View { get; init; } = new();
    public string Unavailable { get; init; } = string.Empty;

    /// <summary>Whether a check finished, could measure, and found something to act on.</summary>
    public bool HasSomethingToRemove =>
        Checked && Unavailable.Length == 0 && !View.RemovesNothing;
}

public sealed record PruneRemoval
{
    public int Repositories { get; init; }
    public int Nodes { get; init; }
    public int Edges { get; init; }

    /// <summary>Queued work for repositories the closure does not reach. Left behind, the crawl
    /// would analyze it and write its nodes straight back.</summary>
    public int Queued { get; init; }
}

/// <summary>What the crawl is trying to achieve, and what it has been spending to get there.</summary>
public sealed record Objective
{
    public IReadOnlyList<DepthProgress> Progress { get; init; } = [];

    /// <summary>
    /// The depth being worked. <see cref="HasOutstanding"/> tells a complete closure from work
    /// at depth 0, which is a different and wrong statement.
    /// </summary>
    public int Working { get; init; }

    public bool HasOutstanding { get; init; }

    /// <summary>The cap exploration may not pass, so "complete" can be told apart from
    /// "complete as far as we are allowed to look".</summary>
    public int MaxDepth { get; init; }

    public IReadOnlyList<CycleSpend> Recent { get; init; } = [];
    public CycleTotal Totals { get; init; } = new();
}

/// <summary>How much of one depth has been settled.</summary>
public sealed record DepthProgress
{
    public int Depth { get; init; }
    public int Total { get; init; }
    public int Done { get; init; }

    /// <summary>Analyzed plus the rows nothing can act on, which is what completeness means.</summary>
    public int Settled { get; init; }

    public int Pending { get; init; }
    public int Running { get; init; }
    public int Available { get; init; }
}

/// <summary>What one cycle cost and what it bought.</summary>
public sealed record CycleSpend
{
    public int Cycle { get; init; }
    public string StartedAt { get; init; } = string.Empty;
    public long Millis { get; init; }

    public int Fetches { get; init; }
    public int Builds { get; init; }
    public int Evaluations { get; init; }
    public int Probes { get; init; }

    public int Analyzed { get; init; }
    public int Skipped { get; init; }
    public int Settled { get; init; }
    public int Discovered { get; init; }
    public int Drifted { get; init; }

    public int ShallowestIncomplete { get; init; }
    public bool HasOutstanding { get; init; }
}

/// <summary>Every recorded cycle summed, so "under budget" has something to check against.</summary>
public sealed record CycleTotal
{
    public int Cycles { get; init; }
    public long Millis { get; init; }
    public int Fetches { get; init; }
    public int Builds { get; init; }
    public int Evaluations { get; init; }
    public int Probes { get; init; }
    public int Analyzed { get; init; }
    public int Discovered { get; init; }
}

/// <summary>
/// How one custom property divides the repositories under review.
///
/// The key travels beside the counts because a reader shown "7 repositories in 3" and not told
/// which property that came from has no way to check it against the organization.
/// </summary>
public sealed record PropertiesView
{
    public string Configured { get; init; } = string.Empty;
    public IReadOnlyList<string> Keys { get; init; } = [];
    public PropertyBreakdown Breakdown { get; init; } = new();
}

/// <summary>The values one property takes, and how many repositories carry each.</summary>
public sealed record PropertyBreakdown
{
    public string Name { get; init; } = string.Empty;
    public int Repositories { get; init; }
    public int WithValue { get; init; }
    public int WithoutValue { get; init; }
    public IReadOnlyList<PropertyCount> Values { get; init; } = [];
    public bool HasDefault { get; init; }
    public string DefaultValue { get; init; } = string.Empty;
}

/// <summary>One value of the grouping property.</summary>
public sealed record PropertyCount
{
    public string Value { get; init; } = string.Empty;
    public int Repositories { get; init; }

    /// <summary>
    /// How many of them have been examined. A repository nothing has queued counts toward the
    /// group and not toward this, because it has not been examined and found clean.
    /// </summary>
    public int Analyzed { get; init; }

    /// <summary>
    /// Supplied by the organization rather than chosen by anybody. The largest group is often this
    /// one, and a reader who is not told cannot tell an inherited value from a decision.
    /// </summary>
    public bool IsDefault { get; init; }
}

/// <summary>One reference and how widely it is used, verbatim and unjudged.</summary>
public sealed record RefUsage
{
    public string Ref { get; init; } = string.Empty;
    public int Count { get; init; }
    public int Repositories { get; init; }

    /// <summary>
    /// The commits the current locks select behind this reference, and whose locks select them:
    /// what runs, beside what the source says. Empty where nothing locks it.
    /// </summary>
    public IReadOnlyList<RefLock> Locked { get; init; } = [];
}

/// <summary>One commit the current locks select behind a declared reference.</summary>
public sealed record RefLock
{
    public string Commit { get; init; } = string.Empty;
    public int Consumers { get; init; }
    public IReadOnlyList<string> Sample { get; init; } = [];
}

/// <summary>
/// One commit a lock selects for a repository's use of a node at one reference: its own lock,
/// through the workflows named, or a caller's, through the pin it reached the composite by.
/// </summary>
public sealed record RepositoryLock
{
    public string Commit { get; init; } = string.Empty;
    public string Consumer { get; init; } = string.Empty;
    public IReadOnlyList<string> Workflows { get; init; } = [];
    public string Via { get; init; } = string.Empty;
}

/// <summary>One repository naming a node at one reference.</summary>
public sealed record RepositoryUse
{
    public string FullName { get; init; } = string.Empty;

    /// <summary>
    /// How the repository entered the corpus, which is provenance and not a claim about who owns
    /// it: <c>seed</c> for one discovery started from, <c>upstream</c> for one fetched only to
    /// resolve what those depend on. Ownership is declared, and is read from the owned scope.
    /// </summary>
    public string Origin { get; init; } = string.Empty;

    public bool Owned { get; init; }

    /// <summary>How many <c>uses:</c> lines, which is how much editing this repository would be.</summary>
    public int Uses { get; init; }

    /// <summary>The commits a current lock selects for these uses instead of the declared reference.</summary>
    public IReadOnlyList<RepositoryLock> Locks { get; init; } = [];

    /// <summary>How many of the uses the repository's own lock governs; the rest run what the reference names.</summary>
    public int LockedUses { get; init; }

    /// <summary>
    /// What the lock reading at the commit the repository currently resolves to recorded —
    /// <c>covered</c>, <c>not_onboarded</c> or <c>unsupported</c>. Empty means nothing was read
    /// there, which is unknown rather than unlocked.
    /// </summary>
    public IReadOnlyList<string> LockCoverage { get; init; } = [];

    /// <summary>
    /// The newest run of GitHub's lock verification at the commit the repository resolves to now.
    /// Null means nothing verified it, which says nothing either way.
    /// </summary>
    public VerificationBrief? Verification { get; init; }
}

/// <summary>Which of GitHub's lock checks ran, what it said, and when.</summary>
public sealed record VerificationBrief
{
    public string Mode { get; init; } = string.Empty;
    public string Outcome { get; init; } = string.Empty;
    public int Findings { get; init; }
    public DateTimeOffset VerifiedAt { get; init; }
}

/// <summary>One run of GitHub's lock verification, as the server recorded it.</summary>
public sealed record LockVerification
{
    public string Repository { get; init; } = string.Empty;
    public string Commit { get; init; } = string.Empty;
    public string Mode { get; init; } = string.Empty;
    public string Outcome { get; init; } = string.Empty;
    public string Error { get; init; } = string.Empty;
}

/// <summary>
/// Every repository naming one node at one exact reference — the rest of the list the Discovery
/// grid can only preview.
///
/// Direct only. Anything reaching the node through an intermediary is <see cref="BumpImpact"/>,
/// and the two are different work rather than two halves of one number.
/// </summary>
public sealed record NodeUsers
{
    public string Key { get; init; } = string.Empty;
    public string Ref { get; init; } = string.Empty;
    public int Total { get; init; }
    public int Seeds { get; init; }
    public int Owned { get; init; }

    /// <summary>
    /// False when no account has been verified, which makes <see cref="Owned"/> an unanswered
    /// question rather than a count of none.
    /// </summary>
    public bool OwnedKnown { get; init; }

    public IReadOnlyList<RepositoryUse> Repos { get; init; } = [];
    public bool Truncated { get; init; }
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

/// <summary>
/// What the graph module tells the page about a hovered node.
///
/// A projection of the graph payload rather than a second source: the canvas keeps a name and one
/// line of context, and everything else is typeset beside the graph. Absent measurements travel as
/// their own flag, because a node ingested since reach was last rebuilt has no measurement and
/// reporting it as zero asserts one nobody made.
/// </summary>
public sealed record GraphNodeBrief
{
    public string Key { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Origin { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;
    public string Owner { get; init; } = string.Empty;
    public string Path { get; init; } = string.Empty;
    public string Container { get; init; } = string.Empty;
    public string Grouping { get; init; } = string.Empty;
    public string Ref { get; init; } = string.Empty;
    public int DependentRepos { get; init; }
    public bool ReachKnown { get; init; }
    public int Findings { get; init; }
    public IReadOnlyList<string> Rules { get; init; } = [];

    /// <summary>Each kind of finding the node carries: rule, producer and severity together.</summary>
    public IReadOnlyList<FindingFacet> Facets { get; init; } = [];
    public int Advisories { get; init; }

    /// <summary>References in use and analyzed. Both zero means nothing ever queued it.</summary>
    public int RefsInUse { get; init; }
    public int RefsAnalyzed { get; init; }
    public string Discovery { get; init; } = string.Empty;
    public int Depth { get; init; }
    public bool IsExternal { get; init; }
}

/// <summary>One kind of finding a graph node carries, and how many findings share it.</summary>
public sealed record FindingFacet
{
    public string Rule { get; init; } = string.Empty;

    /// <summary>Who reported it. Empty where no producer was recorded, never read as CodeQL.</summary>
    public string Provider { get; init; } = string.Empty;

    /// <summary>The level the result reported: error, warning or recommendation.</summary>
    public string Severity { get; init; } = string.Empty;

    /// <summary>The rule's own security score, absent where the rule declares none.</summary>
    public double? SecuritySeverity { get; init; }

    /// <summary>That score read as code scanning reads it, by the server: no copy of the thresholds here.</summary>
    public string Band { get; init; } = string.Empty;
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

    /// <summary>References in use, and how many were analyzed. Analyzed is true at the first one.</summary>
    public int RefsInUse { get; init; }
    public int RefsAnalyzed { get; init; }
    public int DependentRepos { get; init; }
    public int DirectDependents { get; init; }
    public IReadOnlyList<RefUsage> Refs { get; init; } = [];
    public IReadOnlyList<RuleCount> Rules { get; init; } = [];
    public IReadOnlyList<AdvisoryReach> Advisories { get; init; } = [];
    public IReadOnlyList<NodeNeighbor> UsedBy { get; init; } = [];
    public IReadOnlyList<NodeNeighbor> Uses { get; init; } = [];
}

/// <summary>
/// One version of one query, and what is known about it.
///
/// The unit is a version rather than a query: two versions answer differently, so reporting one
/// readiness for both would attach evidence to something that did not produce it.
/// </summary>
public sealed record CatalogQuery
{
    public string QueryId { get; init; } = string.Empty;

    /// <summary>
    /// Content hash of the query and the libraries it imports. Empty for runs recorded before
    /// versions were written, which is why it is never used alone as a key in a message.
    /// </summary>
    public string QueryVersion { get; init; } = string.Empty;
    public string Pack { get; init; } = string.Empty;
    public string PackVersion { get; init; } = string.Empty;
    public string CliVersion { get; init; } = string.Empty;

    /// <summary>Read from the pack that supplied it, never from the rule id's prefix.</summary>
    public string Source { get; init; } = string.Empty;

    /// <summary>
    /// Set when what ran was a whole suite rather than one query, which is how the findings lane
    /// records itself. Kept apart from the query name so a reader can tell a rule from the set it
    /// arrived in.
    /// </summary>
    public string Suite { get; init; } = string.Empty;

    /// <summary>
    /// The query's <c>@kind</c>, empty for a table query. <see cref="KindRecorded"/> keeps that
    /// emptiness apart from a query nobody has resolved a suite for.
    /// </summary>
    public string Kind { get; init; } = string.Empty;
    public bool KindRecorded { get; init; }

    /// <summary>
    /// Whether this builds the dependency graph rather than reporting a finding. Read from the
    /// recorded kind, so a query nobody has described is never hidden for lack of a look.
    /// </summary>
    public bool Discovery { get; init; }

    /// <summary>
    /// Whether anything has run this version's tests, and what they said. Never-tested is its own
    /// answer: a version nobody tested is not one whose tests failed, and only the second is a
    /// defect to go and look at.
    /// </summary>
    public bool Tested { get; init; }
    public bool Passing { get; init; }
    public string TestDetail { get; init; } = string.Empty;

    /// <summary>
    /// Whether this row is a whole-suite invocation rather than a query. The findings lane runs a
    /// suite in one call, so the ledger records the suite as what ran — true, and not a query
    /// anybody develops or versions.
    /// </summary>
    public bool Lane { get; init; }

    /// <summary>Runs somebody asked for against this version, which is what ties a request to
    /// the thing it was testing.</summary>
    public int Analyses { get; init; }
    public string LastAnalysis { get; init; } = string.Empty;

    /// <summary>
    /// Empty when nobody has advanced this version. Absent is not <c>dev</c>: a default would
    /// report a lifecycle decision nobody made.
    /// </summary>
    public string Stage { get; init; } = string.Empty;
    public string StagedBy { get; init; } = string.Empty;
    public string StagedAt { get; init; } = string.Empty;

    /// <summary>
    /// Runs and repositories differ and are never printed as one figure: a query run against two
    /// repositories is two runs and two repositories, and twice against one is two and one.
    /// </summary>
    public int Runs { get; init; }
    public int Repositories { get; init; }
    public string FirstRunAt { get; init; } = string.Empty;
    public string LastRunAt { get; init; } = string.Empty;

    /// <summary>
    /// The version on disk in the operator's pack now: the only one whose tests can be run, and
    /// the one <see cref="Steps"/> describes.
    /// </summary>
    public bool Working { get; init; }

    /// <summary>(b) through (g), served for the working copy only and empty on every other row.</summary>
    public IReadOnlyList<AuthoringStep> Steps { get; init; } = [];

    /// <summary>The requests about this query id, oldest first.</summary>
    public IReadOnlyList<long> Requests { get; init; } = [];

    /// <summary>
    /// What advancing this version to extended-test would be refused with, served so it is said
    /// before anybody presses it. Empty where it would not be refused.
    /// </summary>
    public string ExtendedTestRefusal { get; init; } = string.Empty;

    /// <summary>
    /// What advancing this version to review would be refused with: review is earned by a
    /// comparison of a run of this version with the alerts code scanning holds. Empty where it
    /// would not be refused.
    /// </summary>
    public string ReviewRefusal { get; init; } = string.Empty;

    /// <summary>
    /// Every pair this version declares, as the CLI resolved them. Null where nothing resolved the
    /// version, which is not a query that declares nothing.
    /// </summary>
    public IReadOnlyDictionary<string, string>? Metadata { get; init; }

    /// <summary>The declared score's band, for a query that reports findings; empty otherwise.</summary>
    public string Band { get; init; } = string.Empty;
}

/// <summary>
/// One mark on a query's way through dev and test. The state is served, never derived here: a
/// client that decided its own marks would be a second definition of passing.
/// </summary>
public sealed record AuthoringStep
{
    public string Step { get; init; } = string.Empty;

    /// <summary><c>pending</c>, <c>failed</c>, <c>warning</c> or <c>passing</c>.</summary>
    public string State { get; init; } = string.Empty;

    /// <summary>The evidence behind the state, in words.</summary>
    public string Detail { get; init; } = string.Empty;
}

/// <summary>
/// The catalog and the lifecycle it is staged against. The stages travel with it so the page
/// cannot hold a second, drifting copy of what the lifecycle is.
/// </summary>
public sealed record QueryCatalog
{
    public IReadOnlyList<CatalogQuery> Queries { get; init; } = [];
    public IReadOnlyList<string> Stages { get; init; } = [];
}

/// <summary>One query a run names, tied to the version that was being tested.</summary>
public sealed record AnalysisQueryRef
{
    public string QueryId { get; init; } = string.Empty;
    public string QueryVersion { get; init; } = string.Empty;
}

/// <summary>
/// One repository in a run and what became of it.
///
/// Every repository the scope named has one of these, including the ones nothing could analyze: a
/// run holding only the reachable half reports a clean result for the rest.
/// </summary>
public sealed record AnalysisMember
{
    public string Repository { get; init; } = string.Empty;
    public string Commit { get; init; } = string.Empty;
    public string SourceFilter { get; init; } = string.Empty;
    public string Mode { get; init; } = string.Empty;
    public string Status { get; init; } = string.Empty;

    /// <summary>What went wrong, or why nothing could run.</summary>
    public string Detail { get; init; } = string.Empty;
    public string DatabasePath { get; init; } = string.Empty;
    public string ResultPath { get; init; } = string.Empty;

    /// <summary>
    /// How many findings this member reported, meaningful only where <see cref="Counted"/> is
    /// true. A member nothing ran against has no count, and its zero would read as a repository
    /// the query found nothing in.
    /// </summary>
    public int Results { get; init; }
    public bool Counted { get; init; }

    /// <summary>What each query found here; empty where nothing counted this member.</summary>
    public IReadOnlyDictionary<string, int> ByQuery { get; init; } =
        new Dictionary<string, int>(StringComparer.Ordinal);
    public string StartedAt { get; init; } = string.Empty;
    public string FinishedAt { get; init; } = string.Empty;

    /// <summary>Still to reach in the start under way, which the server decides.</summary>
    public bool ToDo { get; init; }
}

/// <summary>What a run under way has still to do, and a time remaining from its own pace.</summary>
public sealed record RunLeft
{
    public int Members { get; init; }

    /// <summary>The members this start has finished, which the pace is taken from.</summary>
    public int Finished { get; init; }

    /// <summary>Absent until one of them has finished; never a forecast from elsewhere.</summary>
    public long? RemainingSeconds { get; init; }
}

/// <summary>A run somebody asked for, its members and how far it has got.</summary>
public sealed record AnalysisRun
{
    public long RowId { get; init; }

    /// <summary>The predicate the database set was resolved from, verbatim.</summary>
    public string Predicate { get; init; } = string.Empty;
    public string Mode { get; init; } = string.Empty;
    public string RequestedBy { get; init; } = string.Empty;
    public string State { get; init; } = string.Empty;

    /// <summary>Findings off: what its queries report is held until somebody converts it.</summary>
    public bool Held { get; init; }
    public string CreatedAt { get; init; } = string.Empty;
    public string FinishedAt { get; init; } = string.Empty;

    /// <summary>When the start under way, or the last one, began.</summary>
    public string StartedAt { get; init; } = string.Empty;

    /// <summary>The named scope the run was chosen by; empty for one chosen otherwise.</summary>
    public string ScopeName { get; init; } = string.Empty;

    /// <summary>What a pending or running run has left; null for any other.</summary>
    public RunLeft? Left { get; init; }
    public IReadOnlyList<AnalysisQueryRef> Queries { get; init; } = [];
    public IReadOnlyList<AnalysisMember> Members { get; init; } = [];

    /// <summary>What the counted members found between them, totalled by the server.</summary>
    public int Results { get; init; }

    /// <summary>
    /// The CodeQL the run evaluated under, empty for a run recorded before that was kept, or one
    /// that has not resolved yet. A result is CodeQL's as much as the query's.
    /// </summary>
    public string CliVersion { get; init; } = string.Empty;

    /// <summary>Why the run could not start the last time it was asked to; empty otherwise.</summary>
    public string Refusal { get; init; } = string.Empty;

    /// <summary>Every member counted by status, so nothing is left to be inferred from a total.</summary>
    public IReadOnlyDictionary<string, int> Tally { get; init; } =
        new Dictionary<string, int>(StringComparer.Ordinal);
}

public sealed record AnalysisRunList
{
    public IReadOnlyList<AnalysisRun> Runs { get; init; } = [];
}

/// <summary>
/// What a set of a run's results is, against the code scanning alerts GitHub held when compared.
/// Served whole; nothing here is added up in the browser.
/// </summary>
public sealed record ComparisonCounts
{
    public int Results { get; init; }

    /// <summary>Results an open alert already stands for; they add nothing.</summary>
    public int Open { get; init; }

    /// <summary>Results a dismissed alert stands for, which stays dismissed.</summary>
    public int Dismissed { get; init; }

    /// <summary>Results whose alert was fixed, which a developer then sees again.</summary>
    public int Reopens { get; init; }
    public int New { get; init; }

    /// <summary>What a developer would see appear: new and reopened.</summary>
    public int Adds { get; init; }

    /// <summary>Open alerts GitHub held for the rules compared.</summary>
    public int OpenAlerts { get; init; }

    /// <summary>Of those, the ones no result stood for, which stay regardless.</summary>
    public int Missed { get; init; }
}

/// <summary>One rule's counts in a comparison.</summary>
public sealed record ComparisonRule
{
    public string RuleId { get; init; } = string.Empty;
    public int Results { get; init; }
    public int Open { get; init; }
    public int Dismissed { get; init; }
    public int Reopens { get; init; }
    public int New { get; init; }
    public int Adds { get; init; }
    public int OpenAlerts { get; init; }
    public int Missed { get; init; }
}

/// <summary>One repository in a comparison, and what became of it.</summary>
public sealed record ComparisonMember
{
    public string Repository { get; init; } = string.Empty;
    public string AnalyzedCommit { get; init; } = string.Empty;
    public string SourceFilter { get; init; } = string.Empty;

    /// <summary><c>pending</c>, <c>compared</c> or <c>not_compared</c>.</summary>
    public string Status { get; init; } = string.Empty;

    /// <summary>Why a member was not compared, and <see cref="Detail"/> in words.</summary>
    public string Reason { get; init; } = string.Empty;
    public string Detail { get; init; } = string.Empty;

    /// <summary>Whether the run had <see cref="ComparisonCounts.Results"/> results here at all.</summary>
    public bool Counted { get; init; }
    public ComparisonCounts Counts { get; init; } = new();
    public IReadOnlyList<ComparisonRule> Rules { get; init; } = [];

    /// <summary>The commit GitHub's newest Actions analysis describes; empty where none was read.</summary>
    public string AlertedCommit { get; init; } = string.Empty;
    public string AlertedAt { get; init; } = string.Empty;

    /// <summary>The CodeQL GitHub's analysis ran; empty where it was not named.</summary>
    public string AlertedCli { get; init; } = string.Empty;

    /// <summary>The default branch as it was read; empty where it could not be.</summary>
    public string HeadCommit { get; init; } = string.Empty;

    /// <summary><c>current</c>, <c>behind</c>, <c>ahead</c>, <c>diverged</c>, <c>moved</c> or <c>unknown</c>.</summary>
    public string Staleness { get; init; } = string.Empty;

    /// <summary>Commits the default branch has that the analyzed one lacks; null where nobody counted.</summary>
    public int? BehindBy { get; init; }

    /// <summary>Analyzed at the commit, and under the CodeQL, GitHub's alerts describe: a measurement
    /// rather than an inference.</summary>
    public bool Exact { get; init; }
    public string ReadAt { get; init; } = string.Empty;
}

/// <summary>A run's results set against the code scanning alerts GitHub held, member by member.</summary>
public sealed record Comparison
{
    public long RowId { get; init; }
    public long RunId { get; init; }
    public string RequestedBy { get; init; } = string.Empty;

    /// <summary><c>running</c>, <c>complete</c> or <c>interrupted</c>.</summary>
    public string State { get; init; } = string.Empty;
    public string CreatedAt { get; init; } = string.Empty;
    public string FinishedAt { get; init; } = string.Empty;

    /// <summary>The CodeQL the run's results came from.</summary>
    public string CliVersion { get; init; } = string.Empty;
    public IReadOnlyList<AnalysisQueryRef> Queries { get; init; } = [];

    /// <summary>Where alerts appear at all, which every count holds under.</summary>
    public string Condition { get; init; } = string.Empty;

    /// <summary>What the count is a delta over.</summary>
    public string Merge { get; init; } = string.Empty;

    /// <summary>What the count is exact for, and where it becomes an inference.</summary>
    public string Boundary { get; init; } = string.Empty;
    public IReadOnlyList<ComparisonMember> Members { get; init; } = [];

    /// <summary>Over the compared members, and nothing else.</summary>
    public ComparisonCounts Totals { get; init; } = new();
    public IReadOnlyList<ComparisonRule> Rules { get; init; } = [];
    public int Compared { get; init; }
    public int NotCompared { get; init; }
    public int Pending { get; init; }
}

public sealed record ComparisonList
{
    public IReadOnlyList<Comparison> Comparisons { get; init; } = [];
}

/// <summary>One database a run's scope resolved to: held on disk, or analyzed before and not held.</summary>
public sealed record ScopedDatabase
{
    public string Repository { get; init; } = string.Empty;
    public string Commit { get; init; } = string.Empty;
    public string Mode { get; init; } = string.Empty;

    /// <summary><c>retained</c> or <c>buildable</c>.</summary>
    public string Status { get; init; } = string.Empty;

    /// <summary>The CodeQL that built what is held, empty where nothing recorded one.</summary>
    public string CliVersion { get; init; } = string.Empty;

    /// <summary>Why a database that is held resolves as buildable; empty otherwise.</summary>
    public string Reason { get; init; } = string.Empty;
}

/// <summary>A repository a run's scope reached that resolved to no database, and why.</summary>
public sealed record DatabaseGap
{
    public string Repository { get; init; } = string.Empty;
    public string Reason { get; init; } = string.Empty;
}

/// <summary>
/// What a run over a predicate would cover, resolved by the server the way the run itself resolves
/// it. <see cref="Unresolved"/> says why an answer holding nothing holds nothing.
/// </summary>
public sealed record DatabaseSet
{
    public IReadOnlyList<ScopedDatabase> Databases { get; init; } = [];
    public IReadOnlyList<DatabaseGap> Gaps { get; init; } = [];
    public string Mode { get; init; } = string.Empty;
    public string Unresolved { get; init; } = string.Empty;
}

/// <summary>One result a member of a run reported.</summary>
public sealed record RunFinding
{
    /// <summary>The finding; for a held result, the finding somebody converted it to, or zero.</summary>
    public long FindingRowId { get; init; }

    /// <summary>A result a held run kept; zero for a finding a run stored.</summary>
    public long HeldResultId { get; init; }

    /// <summary>Who converted a held result to a finding, and when; empty when nobody has.</summary>
    public string ConvertedBy { get; init; } = string.Empty;
    public string ConvertedAt { get; init; } = string.Empty;
    public string RuleId { get; init; } = string.Empty;
    public string FilePath { get; init; } = string.Empty;
    public int StartLine { get; init; }
    public int StartColumn { get; init; }
    public string Message { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;

    /// <summary>The rule's own score, absent where it declares none, and the band it falls in.</summary>
    public double? SecuritySeverity { get; init; }
    public string Band { get; init; } = string.Empty;

    /// <summary>What the member's evaluation recorded about the rule's query; empty when nothing.</summary>
    public string Source { get; init; } = string.Empty;
    public string RuleVersion { get; init; } = string.Empty;
}

/// <summary>One finding and every evaluation on record as reporting it, oldest first.</summary>
public sealed record FindingTrace
{
    public long RowId { get; init; }
    public string RuleId { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;
    public string FilePath { get; init; } = string.Empty;
    public int StartLine { get; init; }
    public string Message { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;
    public string Provider { get; init; } = string.Empty;
    public IReadOnlyList<FindingEvaluation> Evaluations { get; init; } = [];
}

/// <summary>
/// One evaluation that reported a finding. The rule's version, pack and source are empty where the
/// evaluation did not record them, which is not known rather than none.
/// </summary>
public sealed record FindingEvaluation
{
    public long QueryRunId { get; init; }

    /// <summary>What ran: the query itself, or the suite or queries a lane ran as one.</summary>
    public string Invoked { get; init; } = string.Empty;
    public IReadOnlyList<string> Packs { get; init; } = [];
    public string RuleVersion { get; init; } = string.Empty;
    public string Pack { get; init; } = string.Empty;
    public string PackVersion { get; init; } = string.Empty;
    public string PackDigest { get; init; } = string.Empty;
    public string Source { get; init; } = string.Empty;
    public string CliVersion { get; init; } = string.Empty;
    public string Mode { get; init; } = string.Empty;
    public string Commit { get; init; } = string.Empty;
    public string RanAt { get; init; } = string.Empty;

    /// <summary><c>crawl</c>, <c>run</c> or <c>corpus</c>; empty when it was not recorded.</summary>
    public string RecordedBy { get; init; } = string.Empty;
    public IReadOnlyList<FindingRun> Runs { get; init; } = [];
}

/// <summary>A member of an on-demand run that the evaluation answered.</summary>
public sealed record FindingRun
{
    public long RunId { get; init; }
    public string Repository { get; init; } = string.Empty;
    public string Status { get; init; } = string.Empty;
}

/// <summary>
/// What one member of a run reported. <see cref="Evaluated"/> keeps a repository nothing ran
/// against apart from one that ran and found nothing: both list no findings.
/// </summary>
public sealed record MemberFindings
{
    public string Repository { get; init; } = string.Empty;
    public bool Evaluated { get; init; }

    /// <summary>What a held run kept here: no finding stands behind it until somebody converts it.</summary>
    public bool Held { get; init; }

    /// <summary>
    /// Counted by a run recorded before ABOM kept each repository's own evaluation, so its results
    /// exist but cannot be listed; running it again lists them.
    /// </summary>
    public bool Unlinked { get; init; }
    public IReadOnlyList<RunFinding> Findings { get; init; } = [];
    public bool Truncated { get; init; }
}

/// <summary>What a query should catch or stop catching, kept before any query exists.</summary>
public sealed record QueryRequest
{
    public long RowId { get; init; }
    public string Description { get; init; } = string.Empty;
    public string Compliant { get; init; } = string.Empty;
    public string NonCompliant { get; init; } = string.Empty;

    /// <summary>The query the request is about; empty for one that does not exist yet.</summary>
    public string QueryId { get; init; } = string.Empty;

    /// <summary>
    /// Who chose <see cref="QueryId"/>: <c>operator</c> or <c>agent</c>. Empty beside a target is
    /// a request kept before that was recorded, which is not the operator having chosen it.
    /// </summary>
    public string TargetSource { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Status { get; init; } = string.Empty;
    public string RequestedBy { get; init; } = string.Empty;
    public string CreatedAt { get; init; } = string.Empty;
    public string UpdatedAt { get; init; } = string.Empty;

    /// <summary>The run the request was asked from; zero when it was asked from nothing.</summary>
    public long SourceRunId { get; init; }

    /// <summary>What was picked from that run, as it was filed.</summary>
    public IReadOnlyList<RequestEvidence> Evidence { get; init; } = [];

    /// <summary>(a) the target, then (b) through (g) for the target's working copy.</summary>
    public IReadOnlyList<AuthoringStep> Steps { get; init; } = [];
}

/// <summary>
/// One result picked from a run: a finding that should not have fired, a result a held run kept that
/// should not have, or a repository where the query ran and something should have.
/// </summary>
public sealed record RequestEvidence
{
    public long RunId { get; init; }
    public string Repository { get; init; } = string.Empty;

    /// <summary>Zero for a missed detection, or for a result a held run kept.</summary>
    public long FindingRowId { get; init; }

    /// <summary>The result a held run kept; zero for a finding or a missed detection.</summary>
    public long HeldResultId { get; init; }

    /// <summary><c>false-alarm</c> or <c>missed</c>.</summary>
    public string Kind { get; init; } = string.Empty;
}

/// <summary>The requests, and the words the server accepts for their kind and status.</summary>
public sealed record QueryRequestList
{
    public IReadOnlyList<QueryRequest> Requests { get; init; } = [];
    public IReadOnlyList<string> Kinds { get; init; } = [];
    public IReadOnlyList<string> Statuses { get; init; } = [];
}

/// <summary>
/// Whether a run of some query versions should store what it finds as findings, and why. One
/// setting covers the whole run, so it is on only when every query is promoted.
/// </summary>
public sealed record FindingsSuggestion
{
    public bool Findings { get; init; }
    public string Reason { get; init; } = string.Empty;
    public IReadOnlyList<QuerySuggestion> Queries { get; init; } = [];
}

/// <summary>What one query version contributes to a run's suggestion.</summary>
public sealed record QuerySuggestion
{
    public string QueryId { get; init; } = string.Empty;
    public string QueryVersion { get; init; } = string.Empty;
    public bool Promoted { get; init; }
    public string Why { get; init; } = string.Empty;
}

/// <summary>What converting a held run's results did.</summary>
public sealed record Conversion
{
    public int Converted { get; init; }
    public int Already { get; init; }
}

/// <summary>One agent session on a request, and how much of what it tried was refused.</summary>
public sealed record AuthoringSession
{
    public long RowId { get; init; }
    public long RequestId { get; init; }

    /// <summary><c>running</c>, <c>finished</c>, <c>stopped</c>, <c>failed</c> or <c>interrupted</c>.</summary>
    public string State { get; init; } = string.Empty;
    public string StartedBy { get; init; } = string.Empty;
    public string Model { get; init; } = string.Empty;
    public string StartedAt { get; init; } = string.Empty;
    public string EndedAt { get; init; } = string.Empty;

    /// <summary>How it ended, in words: the agent's last message, or why it stopped.</summary>
    public string Detail { get; init; } = string.Empty;
    public int Events { get; init; }
    public int Refused { get; init; }

    /// <summary>What it spent, each null until the CLI reported it, which is not zero.</summary>
    public long? ModelCalls { get; init; }
    public long? InputTokens { get; init; }
    public long? OutputTokens { get; init; }
    public long? NanoAiu { get; init; }
}

/// <summary>A ql-mcp call the gate approved and has not seen answered: CodeQL at work for the agent.</summary>
public sealed record AuthoringFlight
{
    public string Tool { get; init; } = string.Empty;
    public string Subject { get; init; } = string.Empty;
    public string Step { get; init; } = string.Empty;
    public string Since { get; init; } = string.Empty;
}

public sealed record AuthoringSessionList
{
    public IReadOnlyList<AuthoringSession> Sessions { get; init; } = [];
    public IReadOnlyList<AuthoringFlight> InFlight { get; init; } = [];
}

/// <summary>One hook invocation in a session, as the gate decided or saw it.</summary>
public sealed record AuthoringEvent
{
    public long RowId { get; init; }
    public long SessionId { get; init; }
    public string At { get; init; } = string.Empty;
    public string Hook { get; init; } = string.Empty;
    public string Tool { get; init; } = string.Empty;
    public string Subject { get; init; } = string.Empty;

    /// <summary><c>allowed</c>, <c>refused</c>, <c>review</c>, <c>succeeded</c>, <c>failed</c> or <c>noted</c>.</summary>
    public string Decision { get; init; } = string.Empty;
    public string Step { get; init; } = string.Empty;
    public string Reason { get; init; } = string.Empty;
    public string Detail { get; init; } = string.Empty;

    /// <summary>How long the call ran; null where nothing measured it, which is not zero.</summary>
    public long? TookMs { get; init; }
}

/// <summary>A page of a timeline, and the cursor to read on from.</summary>
public sealed record AuthoringEventPage
{
    public IReadOnlyList<AuthoringEvent> Events { get; init; } = [];
    public long Next { get; init; }
}

/// <summary>
/// What a tool query such as PrintAST printed over a request's test database. The body arrives
/// only when one tree is asked for.
/// </summary>
public sealed record AuthoringAst
{
    public long RowId { get; init; }

    /// <summary>Zero for a tree ABOM printed, which names who asked in <see cref="PrintedBy"/>.</summary>
    public long SessionId { get; init; }
    public long RequestId { get; init; }
    public string PrintedBy { get; init; } = string.Empty;
    public string QueryId { get; init; } = string.Empty;
    public string PrintedAt { get; init; } = string.Empty;
    public string Query { get; init; } = string.Empty;
    public string Database { get; init; } = string.Empty;
    public string SourceFiles { get; init; } = string.Empty;
    public string TestDigest { get; init; } = string.Empty;

    /// <summary>The CodeQL that printed it, empty for a tree kept before that was recorded.</summary>
    public string CliVersion { get; init; } = string.Empty;
    public int Size { get; init; }
    public bool Truncated { get; init; }
    public string Body { get; init; } = string.Empty;
}

public sealed record AuthoringAstList
{
    public IReadOnlyList<AuthoringAst> Asts { get; init; } = [];
}

/// <summary>What one of the target's tests last reported, beside what it is expected to report.</summary>
public sealed record ReviewTest
{
    public string Test { get; init; } = string.Empty;

    /// <summary>Null where the test has reported nothing to compare.</summary>
    public string? Actual { get; init; }
    public string? Expected { get; init; }

    /// <summary>Why the report describes a query or fixtures that have since changed.</summary>
    public string Stale { get; init; } = string.Empty;
}

/// <summary>An operator's decision about a report, with the report they read.</summary>
public sealed record AuthoringReview
{
    public long RowId { get; init; }
    public string QueryId { get; init; } = string.Empty;
    public string QueryVersion { get; init; } = string.Empty;
    public string Actual { get; init; } = string.Empty;

    /// <summary><c>approved</c> or <c>rejected</c>.</summary>
    public string Verdict { get; init; } = string.Empty;
    public string Reason { get; init; } = string.Empty;
    public string ReviewedBy { get; init; } = string.Empty;
    public string ReviewedAt { get; init; } = string.Empty;
}

/// <summary>What a request's target reported, and what has been decided about its reports.</summary>
public sealed record ReviewState
{
    public string QueryId { get; init; } = string.Empty;
    public string QueryVersion { get; init; } = string.Empty;
    public IReadOnlyList<ReviewTest> Tests { get; init; } = [];

    /// <summary>Names the report as read; an approval sends it back.</summary>
    public string Digest { get; init; } = string.Empty;
    public bool Reviewable { get; init; }
    public string Reason { get; init; } = string.Empty;

    /// <summary>
    /// Why this report can be rejected but not accepted: it lists failed expectations, which an
    /// accepted report would make pass. Empty where accepting it is allowed.
    /// </summary>
    public string CannotAccept { get; init; } = string.Empty;
    public IReadOnlyList<AuthoringReview> Reviews { get; init; } = [];

    /// <summary>What ABOM's own test run said, on the answer to an approval.</summary>
    public string Tested { get; init; } = string.Empty;
}

/// <summary>A request an agent drafted from a run's picked results, which nobody has filed.</summary>
public sealed record AgentDraft
{
    public long RowId { get; init; }
    public long SessionId { get; init; }
    public string DraftedAt { get; init; } = string.Empty;
    public string Description { get; init; } = string.Empty;
    public string Compliant { get; init; } = string.Empty;
    public string NonCompliant { get; init; } = string.Empty;
    public string Rationale { get; init; } = string.Empty;
}

/// <summary>A session drafting from a run, and the newest draft it proposed, null until it does.</summary>
public sealed record DraftSession
{
    public long RowId { get; init; }
    public long RunId { get; init; }
    public string QueryId { get; init; } = string.Empty;
    public string State { get; init; } = string.Empty;
    public string StartedBy { get; init; } = string.Empty;
    public string StartedAt { get; init; } = string.Empty;
    public string Detail { get; init; } = string.Empty;
    public int Events { get; init; }
    public AgentDraft? Draft { get; init; }
}

public sealed record DraftSessionList
{
    public IReadOnlyList<DraftSession> Drafts { get; init; } = [];
}
