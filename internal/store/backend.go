package store

import (
	"io"
	"time"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// Backend is the complete persistence boundary; capabilities share one serialized writer.
type Backend interface {
	LifecycleStore
	GraphStore
	EvidenceStore
	DiscoveryStore
	OwnershipStore
	QueryStore
	PolicyStore
	SetupStore
	CheckStore
	TraversalStore
	CoordinationStore
}

// OpenBackend opens the supported SQLite backend without exposing its connection.
func OpenBackend(path string, opts Options) (Backend, error) {
	if IsPostgresTarget(path) {
		s, err := openPostgres(path, opts)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	s, err := Open(path, opts)
	if err != nil {
		return nil, err
	}
	return s, nil
}

type LifecycleStore interface {
	io.Closer
	ReplicationState() (ReplicaState, error)
	Migrate() error
	CreateIndexes() error
	Optimize() error
	SnapshotStore(dir string) (Backend, func() error, error)
	NewWriter(batchSize int) GraphWriter
	ResponseCache() ResponseCache
	MarkDemo(DemoDataset) error
	DemoDataset() (DemoDataset, bool, error)
}

// GraphWriter buffers ingestion through the backend, retaining local/shared node cache semantics.
type GraphWriter interface {
	AddNode(Node) error
	AddEdge(Edge) error
	Flush() error
	ResetLocal()
}

func (s *Store) NewWriter(batchSize int) GraphWriter { return NewWriter(s, batchSize) }

// ResponseCache failures are misses, never failures of the GitHub read it accelerates.
type ResponseCache interface {
	Lookup(url string) (string, []byte, bool)
	Save(url, etag string, body []byte)
}

type GraphStore interface {
	UpsertRepository(fullName, url string) (int64, error)
	InsertUpstreamRepository(fullName string) (int64, error)
	InsertOwnedRepository(fullName string) (int64, error)
	RepositoryOrigin(fullName string) (string, error)
	ReachStaleness() (ReachStaleness, error)
	GraphVersion() (GraphVersion, error)
	RecordGraphGeneration(r GraphRebuild) (int64, error)
	GraphDelta(since int64, opts FullGraphOptions) (GraphDelta, error)
	FullGraph(opts FullGraphOptions) (Graph, error)
	Subgraph(opts SubgraphOptions) (Subgraph, error)
	BillOfMaterials(opts BOMOptions) (BOM, error)
	Summary() (Summary, error)
	SearchNodes(search string, limit int, sc RepositoryScope) ([]NodeMatch, error)
	ActionsInventoryPage(q InventoryQuery) (InventoryPage, error)
	ActionsInventory(search string, limit int, sc RepositoryScope) ([]ActionSummary, error)
	RepositoriesPage(q RepositoriesQuery) (RepositoriesPage, error)
	RepositoriesOverview(search string, limit int, sc RepositoryScope) ([]RepositorySummary, error)
	RepositoryDetailOf(fullName string) (RepositoryDetail, error)
	RepositoriesFor(q RepositoriesQuery) ([]string, error)
	InventoryFindingsPage(q FindingReviewQuery) (FindingsPage, error)
	RelationshipsPage(q RelationshipsQuery) (RelationshipsPage, error)
	WorkflowsPage(q WorkflowsQuery) (WorkflowsPage, error)
	JobsPage(q JobsQuery) (JobsPage, error)
	UnanalyzedDependencies() (int, error)
	NodeDetailOf(key string, at []string) (NodeDetail, error)
	NodeUsersOf(key, ref string) (NodeUsers, error)
	PinningLevel(scope, parent string, f PinningFilter, limit int) ([]PinningRow, error)
	PinningTotals(f PinningFilter) (PinningRow, error)
	FilterValueCounts() (map[string]int, error)
	FilterValues(term string) (FilterValueSet, error)
	NodesMatching(sel FindingSelector) (FindingMatch, error)
	FindingReviewDetails(q FindingReviewQuery, evaluationID int64) (FindingDetails, error)
	DeriveContainment() (int, error)
	Containment() ([]Containment, error)
	ClassifyEdgeBoundaries() (int, error)
	DeriveEdgeFacts() (int, error)
	ActionsReachedThroughComposites() ([]ReachedAction, error)
	ReadReachMark() (ReachMark, bool, error)
	ExplainOutsideClosure(inClosure []string) (PruneReport, error)
	MarkOutsideClosure(inClosure []string) (PruneReport, error)
	RepositoriesOutsideClosure(inClosure []string) ([]string, error)
	Tombstoned() (int, error)
	Compact() (PruneReport, error)
}

type EvidenceStore interface {
	AddAdvisories(advisories []Advisory) (int, error)
	AdvisoriesByReach(limit int, sc RepositoryScope) ([]AdvisoryReach, error)
	ExposuresByRepository(limit int, sc RepositoryScope) ([]Exposure, error)
	ExposureByAdvisory(sc RepositoryScope) ([]ExposureSummary, error)
	AdvisoriesFetchedAt() (time.Time, bool, error)
	AdvisoryPackages() ([]string, error)
	RemediationPriority(limit int, sc RepositoryScope) ([]RemediationItem, error)
	AdvisoriesPage(q AdvisoriesQuery) (AdvisoriesPage, error)
	RecordQueryRun(q QueryRun) (int64, error)
	AttachQueryRunRef(q QueryRun) (bool, error)
	QueryRunRefs(runID int64) ([]string, error)
	AddFindings(findings []Finding) (int, error)
	LinkFindingsToNodes() (int, error)
	AddObservations(runID int64, obs []Observation) (int, error)
	ObservationsOf(subjectKind, subjectKey string) ([]StoredObservation, error)
	QueryRunExists(q QueryRun) (bool, error)
	ShouldProcess(u Unit) (bool, error)
	BeginUnit(u Unit) error
	CompleteUnit(u Unit, rows int) error
	FailUnit(u Unit, cause error) error
	LedgerCounts() (map[string]int, error)
	RecordCommitContent(repository, commit, digest string) error
	CommitContentDigest(repository, commit string) (string, bool, error)
	QueryRunExistsForContent(q QueryRun, digest string) (bool, error)
	HasCommitContentLabeled(repository, label, except string) (bool, error)
	CarryLockReading(repository, commit, digest, reader string) (bool, error)
	RecordLockReading(repository, commit, reader string, rows []LockCoverage, graph LockGraph) error
	LockGraphAt(repository, commit string) (LockGraph, error)
	LockCoverageCounts() (map[string]int, error)
	HasLockCoverage(repository, commit, reader string) (bool, error)
	RequeueLocksReadByAnotherReader(reader string) (int, error)
	LockSelectedDependencies(repository, analyzed string) ([]LockSelection, error)
	LockGovernedEdges() ([]LockGoverned, error)
	RecordLockVerification(v LockVerification) (int64, error)
	LockVerifications(repository string, limit int) ([]LockVerification, error)
	LastLockVerification(repository, commit, mode string) (LockVerification, bool, error)
	LockVerificationsSince(mode string, since time.Time) (int, error)
	CurrentLockfileCommit(repository string) (string, bool, error)
	RecordRefResolution(r RefResolution) error
	SemverForSHA(owner, repo, sha string) (string, bool, error)
	SHAForTag(owner, repo, tag string) (string, bool, error)
	BackfillRefResolutions() (int, error)
	EdgeRunExists(q QueryRun) (bool, error)
	EdgeRunExistsForContent(q QueryRun, digest string) (bool, error)
	MarkSightedContent(runID int64) error
	LiveCommits() (map[string]map[string][]string, error)
	StartingCommits(live map[string]map[string][]string) (map[string]map[string][]string, error)
	EdgeDeclarations(live, starting map[string]map[string][]string) ([]EdgeDeclaration, error)
	UnobservedClosureCommits(fullName string) ([]string, error)
	UnobservedCommitCount() (int, error)
	UnobservedCommits() (map[string]bool, error)
	Reobserve(window int) (Reobservation, error)
	ReobserveInside(window int, inside func(repository string) bool) (Reobservation, error)
	StaleFindings() (StalenessReport, error)
	FindingsAtCommits(key string, commits []string) (map[string]VersionFindings, error)
	QueueStatesOf(key string) (map[string]string, error)
	AdvisoriesNaming(key string) ([]AdvisoryReach, error)
	RepositoriesInScope(owned bool, sc RepositoryScope) (map[string]bool, error)
	FindingsOnLines(key string, counted map[string][]string, live map[string]map[string][]string) (map[string]LineFindings, error)
	FindingTrace(id int64) (FindingTrace, error)
	RecordClosure(m ClosureManifest) error
	ClosureManifestOf(digest string) (ClosureManifest, bool, error)
	RepositoryNamed(fullName string) (int64, string, bool, error)
	AnalyzedCommit(nodeKey, ref string) (string, bool, error)
	ClosureTallyOf(runID int64) (ClosureTally, error)
	SubmissionClosure(fullName string) ([]ClosureEntry, error)
	StartingPointClosure(fullName string) ([]ClosureEntry, error)
	RecordSubmission(r Submission) error
	LastSubmission(repository string) (Submission, error)
	Submissions() ([]Submission, error)
}

type DiscoveryStore interface {
	Frontier(FrontierOptions) Frontier
	RefsCovered() (RefCoverage, error)
	FrontierSnapshot(status string, limit int) ([]FrontierItem, error)
	FrontierRepositoryCounts() (map[string]int, error)
	FrontierCounts() (map[string]int, error)
	UnanalyzedCompositePins() ([]CompositePin, error)
	RequeueStale(cliVersion string, packDigests []string) (int, error)
	RequeueRepository(fullName string) (int, error)
	RetryUnavailable() (UnavailableRetry, error)
	FrontierKeysFor(fullName string) ([]string, error)
	RecordCycle(c CycleSpend) error
	RecentCycles(limit int) ([]CycleSpend, error)
	CycleTotals() (CycleTotal, error)
	SetDiscoveryIntent(running bool) error
	SetDiscoveryResumeOnStart(resume bool) error
	DiscoveryResumeOnStart() (bool, error)
	DiscoveryIntent() (running, known bool, err error)
	AppendDelivery(d EventDelivery) (bool, error)
	Delivery(deliveryID string) (EventDelivery, bool, error)
	PendingDeliveries(after int64, limit int) ([]EventDelivery, error)
	SettleDelivery(id int64, status, reason, detail string) (bool, error)
	DeferDelivery(id int64, reason, detail string) error
	EventLog(failures int) (EventLog, error)
	PruneHistory(before time.Time) (HistoryPruned, error)
	QueueRow(nodeKey, ref string) (QueueRow, bool, error)
	QueueRowsAt(fullName, ref string) ([]QueueRow, error)
	PollDue(since time.Time, limit int) ([]PollCandidate, error)
	ReadPollWork(bound QueueBound) (PollWork, error)
	SetPoolRate(r PoolRate) error
	ClearPoolRate(pool, unit string) error
	PoolRates() ([]PoolRate, error)
	AddPoolSpend(rows []PoolSpend) error
	PoolMeters(since time.Time) ([]PoolMeter, error)
	ReadFreshnessWork(bound QueueBound) (FreshnessWork, error)
	DefaultSetupReadsActions(repository string) (reads, known bool, err error)
	DefaultSetupRanAt(repository, commit string) (bool, error)
	RecordWorkflowRun(r WorkflowRun) (bool, error)
	WorkflowRunFrame() (WorkflowRunFrame, error)
	RecentWorkflowRuns(limit int) ([]WorkflowRun, error)
	RequeueTier(tier string) (int, error)
	TierCounts() (map[string]TierCount, error)
	TierSuites() (map[string][]string, error)
	SetTierSuites(tier string, suites []string) error
	RecordSuiteContents(suite string, queries []SuiteQuery) error
	SuiteContents() (map[string][]SuiteQuery, error)
	SetToolPath(tool, path, by string) error
	ToolPaths() (map[string]ToolPath, error)
}

type OwnershipStore interface {
	ReplaceAccounts(v Verification) error
	Accounts() (Verification, error)
	ForgetAccounts() error
	DeclaredOwners() (map[string]bool, error)
	Verified() (bool, error)
	OwnerCandidates() ([]OwnerCandidate, error)
	RecordInstallationSync(sync InstallationSync) error
	InstallationEvidence(owners []string) (map[string]*InstallationEvidence, error)
	SetDefaultBranch(fullName, branch string) error
	DefaultBranch(fullName string) (string, bool, error)
	SetProtectedBranches(fullName string, branches []string) (BranchQueue, error)
	ProtectedBranches(fullName string) ([]string, bool, error)
	UpsertCatalogEntry(e CatalogEntry) error
	CatalogRepositories(language string) ([]string, error)
	CatalogCoverage(language string) (withDB, total int, err error)
	CatalogCommit(repository, language string) (string, bool, error)
	RecordRepositoryListing(owner string, seen []string, full bool) (int, error)
	RecordPropertyJudgement(owner string, unjudged int) error
	EnumerationState(owner string) (EnumerationMarks, bool, error)
	RecordFullListing(owner, route string, pages int, highCreated, highPushed time.Time) error
	RecordSweep(owner, route string, highCreated, highPushed time.Time) error
	MissingRepositories() (map[string]bool, error)
	SetOwnedScope(accounts []DeclaredAccount, by string) error
	PropertyScopeMatches(owner string, terms []PropertyTerm) (int, error)
	OwnedScope() ([]DeclaredAccount, error)
	JudgePropertyScopes(accounts []DeclaredAccount) error
	OwnedScopeDeclared() (bool, error)
	AllRepositories() ([]string, error)
	OwnedRepositories() ([]string, error)
	RepositoryIsOwned(fullName string) (bool, error)
	OwnedAmong(fullNames []string) (map[string]bool, error)
	ReplacePropertyValues(repository string, values []PropertyValue) error
	ForgetPropertyValues(repositories []string) error
	PropertyValuesByRepository() (map[string][]PropertyValue, error)
	PropertyValues(repository string) ([]PropertyValue, error)
	PropertyBreakdown(name string) (PropertyBreakdown, error)
	SourceRepositoriesFor(include, exclude []viewspec.PropertyMatch) ([]string, error)
	PropertyKeys() ([]string, error)
	CodeQLConfigFile(repository string) (string, error)
	ReplacePropertySchema(owner string, defs []PropertyDefinition) error
	PropertySchema(owner string) ([]PropertyDefinition, error)
	PropertySchemaReadAt(owner string) (time.Time, bool, error)
}

type QueryStore interface {
	InterruptAbandonedRuns() (int, error)
	CreateAnalysisRun(req AnalysisRequest) (int64, error)
	SetAnalysisMemberStatus(runID int64, id DatabaseIdentity, status, detail string) error
	RecordAnalysisMemberResult(runID int64, id DatabaseIdentity, r MemberResult) error
	SetAnalysisMemberDatabase(runID int64, from, to DatabaseIdentity, path string) error
	AnsweredBy(q QueryRun) (int64, MemberResult, bool, error)
	SetAnalysisRunCodeQL(runID int64, cli string) error
	RefuseAnalysisRun(runID int64, reason string) error
	SetAnalysisRunState(runID int64, state string) error
	AnalysisRun(id int64) (AnalysisRun, error)
	AnalysisRuns() ([]AnalysisRun, error)
	RecordHeldMemberResult(runID int64, id DatabaseIdentity, r MemberResult, held []Finding) error
	ConvertHeld(runID int64, repository string, ids []int64, by string) (Conversion, error)
	RecordAuthoringAST(a AuthoringAST) (int64, error)
	AuthoringASTs(request int64) ([]AuthoringAST, error)
	QueryASTs(queryID string) ([]AuthoringAST, error)
	AuthoringAST(id int64) (AuthoringAST, error)
	NewestASTs() (map[string]AuthoringAST, error)
	StartDraftSession(run int64, queryID, startedBy, model string) (int64, error)
	RecordRequestDraft(d RequestDraft) (int64, error)
	CheckPicked(run int64, queryID string, evidence []RequestEvidence) error
	DraftSessions(run int64) ([]DraftSession, error)
	RecordAuthoringReview(r AuthoringReview) (int64, error)
	AuthoringReviews(request int64) ([]AuthoringReview, error)
	StartAuthoringSession(request int64, startedBy, model string) (int64, error)
	EndAuthoringSession(session int64, state, detail string) error
	InterruptAuthoringSessions() (int, error)
	AuthoringSessions(request int64) ([]AuthoringSession, error)
	RunningAuthoringSessions() (map[int64]int64, error)
	AddAuthoringSpend(session int64, spent AuthoringSpend) error
	RecordAuthoringEvent(e AuthoringEvent) (int64, error)
	AuthoringEvents(session, after int64, limit int) ([]AuthoringEvent, error)
	DecideTarget(request int64, queryID, rationale string) error
	CreateComparison(runID int64, by string) (int64, error)
	PendingComparisonMembers(id int64) ([]DatabaseIdentity, error)
	MemberResults(runID int64, id DatabaseIdentity) ([]ComparedResult, error)
	RecordComparisonMember(id int64, r ComparisonRecord) error
	FinishComparison(id int64) error
	InterruptAbandonedComparisons() (int, error)
	InterruptComparison(id int64) error
	ComparisonsOf(runID int64) ([]Comparison, error)
	Comparison(id int64) (Comparison, error)
	ComparisonResults(id int64, repository string) ([]ComparedResult, error)
	SuggestFindings(queries []AnalysisQuery) (FindingsSuggestion, error)
	RetainDatabase(d RetainedDatabase) (int64, error)
	RetainedDatabaseAt(id DatabaseIdentity) (RetainedDatabase, bool, error)
	UseDatabase(id DatabaseIdentity) (bool, error)
	RetainedDatabases() ([]RetainedDatabase, error)
	RetainedDatabaseBytes() (int64, error)
	EvictDatabasesOver(budget int64) ([]RetainedDatabase, error)
	QueryCatalog(working ...WorkingQuery) ([]CatalogQuery, error)
	AdvanceQueryStage(queryID, version, stage, by, cli string) error
	DeployedQueryIDs() ([]string, error)
	RecordQueryMetadata(mds []QueryMetadata) error
	SaveQueryRequest(r QueryRequest) (int64, error)
	DeleteQueryRequest(id int64) error
	QueryRequests() ([]QueryRequest, error)
	RecordQueryTest(t QueryTest) error
	QueryTestFor(queryID, version, cli string) (QueryTest, bool, error)
	QueryTestsOf(queryID string) ([]QueryTest, error)
	ReviewRefusal(queryID, version, cli string) (string, error)
	ReviewRefusals(cli string) (ReviewRefusals, error)
	RunFindings(runID int64, repository, queryID string, limit int) (MemberFindings, error)
	DatabasesMatching(sel FindingSelector) (DatabaseSet, error)
	DatabasesListed(q RepositoriesQuery, sel FindingSelector) (DatabaseSet, error)
}

type PolicyStore interface {
	SavePolicy(p Policy) error
	PolicyNamed(name string) (Policy, bool, error)
	Policies() ([]Policy, error)
	RecordSimulation(run Simulation) (int64, error)
	LatestSimulation(policy string) (Simulation, bool, error)
	AdoptPolicy(name, by string) error
	GrantWaiver(w Waiver) error
	Waivers(policy string) ([]Waiver, error)
	AllWaivers() ([]Waiver, error)
	WaivedRepositories(policy string) (map[string]Waiver, error)
	EvaluatePolicy(name, unitProperty string) (PolicyOutcome, error)
	SimulatePolicy(name, unitProperty string) (PolicyOutcome, Simulation, error)
	MigratePolicyScopes(unitProperty string) (PolicyScopeMigration, error)
	SaveView(v SavedView) error
	SavedViews() ([]SavedView, error)
	SavedViewNamed(name string) (SavedView, bool, error)
	DeleteView(name string) error
}

type SetupStore interface {
	RecordListedFacts(facts []ListedFacts, at time.Time) error
	ListedFactsOf(repository string) (ListedFacts, bool, error)
	RecordSetupReading(r SetupReading) error
	RecordActionsPolicy(repository, policy string, at time.Time) error
	RecordAnalysesSummary(a AnalysesSummary) error
	ReplaceSecurityConfigurations(account string, at time.Time, configs []SecurityConfiguration, attachments []ConfigurationAttachment) error
	RecordConfigurationsUnread(account string, at time.Time, reason string) error
	SetupClasses(names ...string) ([]SetupClass, error)
	RecordSetupPreview(p SetupPreview) (int64, error)
	ApproveSetupPreview(id int64, by string, at time.Time) error
	StartSetupBatch(previewID int64, by string, at time.Time) (int64, error)
	SetSetupBatchState(id int64, state, reason string, at time.Time) error
	SetupBatches() ([]SetupBatch, error)
	SetupBatchOf(id int64) (SetupBatch, bool, error)
	SetupBatchRemaining(batch int64) ([]SetupPreviewRow, error)
	AppendSetupWrite(w SetupWrite) error
	SetupLedger(repository string, batch int64) ([]SetupWrite, error)
	SetupLedgerNewest(repository string, batch int64, limit int) (SetupLedgerPage, error)
	SetupWritesInFlight() ([]SetupWrite, error)
	SetupPreviews() ([]SetupPreview, error)
	SetupPreviewOf(id int64) (SetupPreview, bool, error)
	SetupPreviewRowsPage(id int64, limit, offset int) (SetupPreviewRows, error)
	RecordRepositoryConfiguration(repository string, cfg *SecurityConfiguration, status string, at time.Time) error
	SetupBatchState(id int64) (string, error)
	SetupDeferred(batch int64) (map[string]SetupDeferral, error)
}

type CheckStore interface {
	DeclaredCommits() ([]DeclaredCommit, error)
	CommitDeclarations(repository string, commits []string) ([]Declaration, error)
	DerivedRows() ([]DerivedTable, error)
	RepositoryReach() (reach, exposure map[string]map[string]bool, err error)
	StoredReach() (reach, versions map[int64]map[int64]int, err error)
	RepositoryNames() (map[int64]string, error)
	RollupLevels() ([]RollupLevel, error)
	ReachOutsideStartingPoints() (int, []ReachRow, error)
	KindAgreement() (KindAgreement, error)
	LiveCommitEvidence() ([]LiveCommitEvidence, error)
	LinesInScope() (int, error)
	QueueClaims() ([]QueueClaim, error)
	MigrateChecks() error
	RecordCheckRun(run CheckRun) (int64, error)
	LatestCheckRuns() (map[int]CheckRun, error)
	CheckTrend() ([]CheckTrendPoint, error)
	OpenDisagreements(offset, limit int) (DisagreementPage, error)
	OpenSubjects(check int) ([]CheckSubject, error)
}

var _ Backend = (*Store)(nil)
var _ GraphWriter = (*Writer)(nil)
var _ ResponseCache = (*HTTPCache)(nil)
