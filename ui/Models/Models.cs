namespace Abom.WebAssembly.Models;

/// <summary>State of every stage, which is only meaningful read together.</summary>
public sealed record Summary
{
    public int SeedRepos { get; init; }

    /// <summary>
    /// Seeded repositories the declaration claims. Independent of <see cref="SeedRepos"/>: a
    /// corpus can hold hundreds and own none of them, so the two are never printed as one figure.
    /// </summary>
    public int OwnedRepos { get; init; }
    public int UpstreamRepos { get; init; }
    public int Nodes { get; init; }

    /// <summary>
    /// Nodes that are something depended on, rather than part of the thing doing the depending.
    /// <see cref="Nodes"/> counts steps, jobs and workflows too and they outnumber these fifty to
    /// one, so the two are never printed under the same word.
    /// </summary>
    public int Dependencies { get; init; }
    public int Edges { get; init; }
    public int Findings { get; init; }

    /// <summary>
    /// Findings in repositories the declaration claims. Bounded for the reason
    /// <see cref="OwnedRepos"/> is: a finding in a repository nobody declared is not in what you
    /// run, and printing the corpus total as yours offers work you cannot do.
    /// </summary>
    public int OwnedFindings { get; init; }
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

    /// <summary>
    /// Repositories the ledger already had on record. Analyzed cannot stand in for it, so a
    /// re-analysis of a current corpus would otherwise move no number at all.
    /// </summary>
    public int Skipped { get; init; }
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

/// <summary>
/// What a selective requeue put back, beside the state it left behind.
///
/// The identity travels with the count because the count alone cannot be checked: "1,742
/// requeued" is a number, "against CodeQL 2.26.4 and 4 packs" is a claim anyone can test.
/// </summary>
public sealed record RequeueResult
{
    public int Requeued { get; init; }
    public int Expedited { get; init; }
    public string CliVersion { get; init; } = string.Empty;
    public int Packs { get; init; }
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

    /// <summary>Empty when advisories were never collected.</summary>
    public string AdvisoriesFetchedAt { get; init; } = string.Empty;

    public int Pending => Frontier.TryGetValue("pending", out var n) ? n : 0;
    public int Running => Frontier.TryGetValue("running", out var n) ? n : 0;
    public int Done => Frontier.TryGetValue("done", out var n) ? n : 0;

    public int Count(string state) => Frontier.TryGetValue(state, out var n) ? n : 0;

    /// <summary>Everything not yet looked inside, however many attempts it has cost.</summary>
    public int Unresolved => Count("queued") + Count("retrying") + Running;
}

public sealed record FrontierItem
{
    public string NodeKey { get; init; } = string.Empty;

    /// <summary>
    /// The reference this row analyzes, empty for the default branch. The queue is keyed
    /// (node_key, ref), so without this a row cannot say which version it is.
    /// </summary>
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

    /// <summary>
    /// What analyzed this row, as <c>pack@version</c>, taken from the runs recorded against its
    /// commit rather than from the tier's current binding.
    /// </summary>
    public IReadOnlyList<string> Suites { get; init; } = Array.Empty<string>();

    /// <summary>
    /// False when nothing recorded which suites ran. Most analyzed rows predate pack identity,
    /// and "not recorded" must not render as "nothing looked" — those are opposite statements.
    /// </summary>
    public bool SuitesKnown { get; init; }

    /// <summary>The CodeQL that produced the row, empty when unrecorded.</summary>
    public string CliVersion { get; init; } = string.Empty;

    /// <summary>
    /// Repositories running this exact version, a bounded preview. <see cref="UsedByCount"/> is
    /// the real total; the queue's DiscoveredFrom is only the first to reach it.
    /// </summary>
    public IReadOnlyList<string> UsedBy { get; init; } = Array.Empty<string>();

    /// <summary>How many repositories name this version in total.</summary>
    public int UsedByCount { get; init; }

    /// <summary>
    /// How many of them the operator is verified against. These are the ones they can change
    /// themselves, which is a different decision from the total.
    /// </summary>
    public int UsedByOwned { get; init; }

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

/// <summary>A question worth asking repeatedly, named. A tab is one of these, not a page.</summary>
///
/// A built-in ships with the product; a saved one is an operator's own. `Builtin` is what tells a
/// reader which they are looking at, and the two sit side by side deliberately.
public sealed record NamedView
{
    public string Name { get; init; } = string.Empty;
    public string Title { get; init; } = string.Empty;
    public string Predicate { get; init; } = string.Empty;
    public bool Builtin { get; init; }
    public string CreatedBy { get; init; } = string.Empty;

    /// <summary>Terms Inventory cannot narrow by, so a tab that will refuse itself can say so.</summary>
    public IReadOnlyList<string> ListingRefuses { get; init; } = [];

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
}

public sealed record JobsPage
{
    public IReadOnlyList<JobRow> Items { get; init; } = [];
    public int Total { get; init; }
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
/// <c>repository.origin</c> records how a repository entered the corpus and <see
/// cref="OwnedAccount"/> records what GitHub says you belong to. Neither means anyone can change
/// anything.
/// </summary>
public sealed record DeclaredAccount
{
    public string Owner { get; init; } = string.Empty;

    /// <summary><c>organization</c> or <c>user</c>. A user account is opted into.</summary>
    public string Kind { get; init; } = string.Empty;

    public bool Enabled { get; init; }

    /// <summary>
    /// True when the account is restricted to <see cref="Includes"/>. Held rather than derived
    /// from the list being non-empty, so removing the last include cannot silently widen an
    /// account from two repositories to all of them.
    /// </summary>
    public bool IncludesOnly { get; init; }

    public IReadOnlyList<string> Includes { get; init; } = [];
    public string DeclaredAt { get; init; } = string.Empty;

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

    /// <summary>True when gh answered and did not list this account. Not a gap in evidence —
    /// evidence against, which is why it may not carry an ownership mark.</summary>
    public bool Contradicted => State == "contradicted";

    /// <summary>True when nobody can say how this declaration got here.</summary>
    public bool Unaccounted => string.IsNullOrEmpty(DeclaredBy);
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
/// <remarks>
/// Declaring names accounts; enumerating turns them into repositories. The two were separate for
/// a week, and a corpus was found where the operator had declared both accounts they owned — 397
/// repositories — and it held one, arrived at as somebody else's dependency.
/// </remarks>
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

/// <summary>
/// A bill of materials: what a set of roots is composed of, and what was examined to say so.
///
/// Coverage travels inside the document rather than beside it, because a reader holding only this
/// has to be able to tell a short document from a thorough one.
/// </summary>
public sealed record Bom
{
    public IReadOnlyList<string> Roots { get; init; } = [];
    public string GeneratedAt { get; init; } = string.Empty;
    public IReadOnlyList<BomComponent> Components { get; init; } = [];
    public IReadOnlyList<BomRelationship> Relationships { get; init; } = [];
    public BomCoverage Coverage { get; init; } = new();
}

/// <summary>One thing the roots are composed of.</summary>
public sealed record BomComponent
{
    public string Key { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;

    /// <summary>Relative to this document's roots, which is why it is not a fact about the edge.</summary>
    public bool Direct { get; init; }

    public int Depth { get; init; }
    public IReadOnlyList<BomReference> References { get; init; } = [];
    public IReadOnlyList<BomFinding> Findings { get; init; } = [];
    public IReadOnlyList<BomAdvisory> Advisories { get; init; } = [];

    /// <summary>
    /// At least one reference in use was examined. The pair below says how much: one boolean
    /// cannot distinguish partial coverage from complete.
    /// </summary>
    public bool Analyzed { get; init; }

    public int RefsInUse { get; init; }
    public int RefsAnalyzed { get; init; }
}

/// <summary>A version of a component as some workflow actually wrote it.</summary>
public sealed record BomReference
{
    public string Raw { get; init; } = string.Empty;
    public string Value { get; init; } = string.Empty;
    public string ResolvedSha { get; init; } = string.Empty;
    public bool Analyzed { get; init; }
}

/// <summary>One result, carrying whatever provenance was recorded for it.</summary>
public sealed record BomFinding
{
    public string RuleId { get; init; } = string.Empty;
    public string Repository { get; init; } = string.Empty;
    public string FilePath { get; init; } = string.Empty;
    public int StartLine { get; init; }
    public string Message { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;
    public double SecuritySeverity { get; init; }

    /// <summary>Empty means the run was not recorded, which is a state and not a failure.</summary>
    public string RunKey { get; init; } = string.Empty;

    public string PackDigest { get; init; } = string.Empty;
    public string ResultHash { get; init; } = string.Empty;
    public string CliVersion { get; init; } = string.Empty;
}

/// <summary>
/// An advisory naming a component's repository, reported beside the observed references and
/// never applied to them.
/// </summary>
public sealed record BomAdvisory
{
    public string GhsaId { get; init; } = string.Empty;
    public string Severity { get; init; } = string.Empty;
    public string Summary { get; init; } = string.Empty;
    public string VulnerableRange { get; init; } = string.Empty;
}

/// <summary>A dependency between two components.</summary>
public sealed record BomRelationship
{
    public string Source { get; init; } = string.Empty;
    public string Target { get; init; } = string.Empty;
    public string Kind { get; init; } = string.Empty;
    public string Boundary { get; init; } = string.Empty;
}

/// <summary>What was examined to produce the document.</summary>
public sealed record BomCoverage
{
    public int ComponentsInUse { get; init; }
    public int ComponentsAnalyzed { get; init; }
    public int ReferencesInUse { get; init; }
    public int ReferencesAnalyzed { get; init; }
    public int Findings { get; init; }

    /// <summary>
    /// Analyzed but not attributable to a recorded run. A real third state: 2,759 findings on
    /// the reference corpus are in it.
    /// </summary>
    public int FindingsUntraced { get; init; }

    public int FindingsWithoutHash { get; init; }
    public int FindingsWithoutDigest { get; init; }
}

/// <summary>One reference and how widely it is used, verbatim and unjudged.</summary>
public sealed record RefUsage
{
    public string Ref { get; init; } = string.Empty;
    public int Count { get; init; }
    public int Repositories { get; init; }
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
    public int Advisories { get; init; }

    /// <summary>References in use and analyzed. Both zero means nothing ever queued it.</summary>
    public int RefsInUse { get; init; }
    public int RefsAnalyzed { get; init; }
    public string Discovery { get; init; } = string.Empty;
    public int Depth { get; init; }
    public bool IsExternal { get; init; }
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
