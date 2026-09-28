package expand

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// absentCommit is the commit on record for the member with no database, spelled as git spells one.
const absentCommit = "0123456789abcdef0123456789abcdef01234567"

// executorFixture is a run over three repositories: two with a database on disk, and one with none
// whose repository cannot be read, so nothing can be built or analyzed for it.
func executorFixture(t *testing.T) (*store.Store, *Executor, int64, *fakeAnalyzer) {
	t.Helper()
	h, e, id, analyzer := executorHarness(t)
	return h.store, e, id, analyzer
}

// executorHarness is executorFixture with the fakes behind the crawl, for tests that change what
// a fetch or a build does.
func executorHarness(t *testing.T) (*harness, *Executor, int64, *fakeAnalyzer) {
	t.Helper()
	h := newHarness(t)
	h.cli = "2.26.4"
	h.fetchErr["octo/absent"] = fmt.Errorf("%w: octo/absent has no workflows or action manifest",
		ErrUnavailable)
	root := t.TempDir()
	query := queryInPack(t, root, "codeql/actions-queries", "0.6.35", "Unpinned.ql", "select 1")

	analyzer := &fakeAnalyzer{}
	e := &Executor{
		Store: h.store,
		Resolver: &fakeResolver{
			queries: []string{query},
			meta:    map[string]runner.Metadata{query: {ID: "actions/unpinned-tag", Kind: "problem"}},
		},
		Analyzer: analyzer,
		Ingester: h.expander.Ingester,
		Crawl:    h.expander,
		Suites:   []string{"abom-findings.qls"},
		WorkDir:  filepath.Join(root, "runs"),
	}

	held := func(repo, sha string) store.ScopedDatabase {
		dir := filepath.Join(root, "db", repo)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("preparing %s: %v", dir, err)
		}
		// The CLI records which CodeQL built a database, and a run evaluates only its own.
		if err := os.WriteFile(filepath.Join(dir, "codeql-database.yml"),
			[]byte("creationMetadata:\n  cliVersion: 2.26.4\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return store.ScopedDatabase{
			DatabaseIdentity: store.DatabaseIdentity{
				Repository: repo, Commit: sha, SourceFilter: "f0", Mode: store.AnalysisModeRegular,
			},
			Status: store.DatabaseRetained, Path: dir,
		}
	}
	id, err := h.store.CreateAnalysisRun(store.AnalysisRequest{
		RequestedBy: "operator",
		Predicate:   "rules=actions%2F*",
		Mode:        store.AnalysisModeRegular,
		Queries:     []store.AnalysisQuery{{QueryID: "actions/unpinned-tag"}},
		Databases: []store.ScopedDatabase{
			held("octo-app", "sha-one"),
			held("octo-tool", "sha-two"),
			{DatabaseIdentity: store.DatabaseIdentity{
				Repository: "octo/absent", Commit: absentCommit,
				SourceFilter: "f0", Mode: store.AnalysisModeRegular,
			}, Status: store.DatabaseBuildable},
		},
	})
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	return h, e, id, analyzer
}

// runOver records a run of the fixture's query over the given databases and gaps.
func runOver(
	t *testing.T, s *store.Store, mode string, dbs []store.ScopedDatabase, gaps []store.DatabaseGap,
) int64 {
	t.Helper()
	id, err := s.CreateAnalysisRun(store.AnalysisRequest{
		RequestedBy: "operator", Predicate: "repositories=octo%2Fx", Mode: mode,
		Queries:   []store.AnalysisQuery{{QueryID: "actions/unpinned-tag"}},
		Databases: dbs, Gaps: gaps,
	})
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	return id
}

func membersByRepository(t *testing.T, s *store.Store, id int64) map[string]store.AnalysisMember {
	t.Helper()
	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	by := map[string]store.AnalysisMember{}
	for _, m := range run.Members {
		by[m.Repository] = m
	}
	return by
}

// The run analyzes what is on disk and says so per repository, which is the whole point of
// retaining databases.
func TestARunAnalyzesEveryMemberWhoseDatabaseIsHeld(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	by := membersByRepository(t, s, id)
	for _, repo := range []string{"octo-app", "octo-tool"} {
		if by[repo].Status != store.AnalysisSucceeded {
			t.Errorf("%s is %q, want %q", repo, by[repo].Status, store.AnalysisSucceeded)
		}
	}
	if len(analyzer.targets) != 2 {
		t.Errorf("analyze ran %d times, want once per held database", len(analyzer.targets))
	}
}

// A result names the CodeQL that ran it, extraction included. A held database another CodeQL
// built would put that CodeQL's extraction under this one's name, so the run builds it again.
func TestARunRebuildsAHeldDatabaseAnotherCodeQLBuilt(t *testing.T) {
	h, e, _, analyzer := executorHarness(t)
	stale := filepath.Join(t.TempDir(), "held-by-2.26.3")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "codeql-database.yml"),
		[]byte("creationMetadata:\n  cliVersion: 2.26.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	analyzer.failOn = stale
	id := runOver(t, h.store, store.AnalysisModeRegular, []store.ScopedDatabase{{
		DatabaseIdentity: store.DatabaseIdentity{Repository: "octo/app", Commit: absentCommit,
			SourceFilter: "f0", Mode: store.AnalysisModeRegular},
		Status: store.DatabaseRetained, Path: stale,
	}}, nil)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := membersByRepository(t, h.store, id)["octo/app"]; got.Status != store.AnalysisSucceeded {
		t.Errorf("octo/app is %q (%s), want it rebuilt under 2.26.4 and analyzed", got.Status, got.Detail)
	}
	if h.databasesBuilt() != 1 {
		t.Errorf("built %d databases, want the one CodeQL 2.26.3 had built", h.databasesBuilt())
	}
	if run, err := h.store.AnalysisRun(id); err != nil || run.CLIVersion != "2.26.4" {
		t.Errorf("the run names CodeQL %q (%v), want the 2.26.4 that carried it out", run.CLIVersion, err)
	}
}

// A member whose database cannot be built says why and stays uncounted: a completed run must never
// read as a clean result for a repository nothing ran against.
func TestAMemberWhoseDatabaseCannotBeBuiltSaysWhy(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	absent := membersByRepository(t, s, id)["octo/absent"]
	if absent.Status != store.AnalysisFailed {
		t.Errorf("a member that cannot be built is %q, want %q", absent.Status, store.AnalysisFailed)
	}
	if !strings.Contains(absent.Detail, "no workflows or action manifest") {
		t.Errorf("the member says %q, want the reason its repository could not be built", absent.Detail)
	}
	if absent.Counted {
		t.Error("a member nothing ran against reports a count")
	}
}

// A run covers the scope it names: a member with no database gets one built the way the crawl
// builds one, and its results name the database they came from.
func TestAMemberWithNoDatabaseIsBuiltAndThenAnalyzed(t *testing.T) {
	h, e, id, analyzer := executorHarness(t)
	delete(h.fetchErr, "octo/absent")

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	absent := membersByRepository(t, h.store, id)["octo/absent"]
	if absent.Status != store.AnalysisSucceeded || !absent.Counted || absent.Results != 2 {
		t.Errorf("the built member is %q counted=%v results=%d, want the 2 its SARIF reported",
			absent.Status, absent.Counted, absent.Results)
	}
	if absent.Commit != absentCommit {
		t.Errorf("the member was built at %q, want the commit on record %q", absent.Commit, absentCommit)
	}
	if absent.SourceFilter != h.expander.sourceFilter() {
		t.Errorf("the member names filter %q, want %q, the one its tree was fetched under",
			absent.SourceFilter, h.expander.sourceFilter())
	}
	if built := h.databasesBuilt(); built != 1 {
		t.Errorf("the crawl's builder ran %d times, want once for the member with no database", built)
	}
	if len(analyzer.targets) != 3 {
		t.Errorf("analyze ran %d times, want once per member", len(analyzer.targets))
	}

	var commit, filter string
	if err := h.store.DB().QueryRow(`
		SELECT db_commit, source_filter FROM abom_query_run WHERE repository = 'octo/absent'`).
		Scan(&commit, &filter); err != nil {
		t.Fatalf("no evaluation of the built member is on record: %v", err)
	}
	if commit != absent.Commit || filter != absent.SourceFilter {
		t.Errorf("the evaluation names %s under %s, want the database the member was built at",
			commit, filter)
	}
}

// Retained for the next run, which is the tuning loop the cache exists for: after an edit, a run
// over the same scope analyzes again and neither fetches nor extracts.
func TestABuiltDatabaseIsKeptForTheNextRun(t *testing.T) {
	h, e, id, _ := executorHarness(t)
	delete(h.fetchErr, "octo/absent")
	h.retainCheckouts()
	h.retainDatabases()

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	absent := membersByRepository(t, h.store, id)["octo/absent"]
	held, ok, err := h.store.RetainedDatabaseAt(absent.DatabaseIdentity)
	if err != nil || !ok {
		t.Fatalf("the database the run built is not retained: %v", err)
	}
	if absent.DatabasePath != held.Path {
		t.Errorf("the member names %q, want the retained database at %q", absent.DatabasePath, held.Path)
	}

	editQuery(t, e)
	built, fetched := h.databasesBuilt(), h.fetchCount()
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run again: %v", err)
	}
	if h.databasesBuilt() != built || h.fetchCount() != fetched {
		t.Errorf("running again built %d and fetched %d, want neither",
			h.databasesBuilt()-built, h.fetchCount()-fetched)
	}
	if again := membersByRepository(t, h.store, id)["octo/absent"]; again.Status != store.AnalysisSucceeded {
		t.Errorf("the member run again is %q, want %q", again.Status, store.AnalysisSucceeded)
	}
}

// editQuery changes the fixture's query, and so its version, which is what makes a run need its
// databases again rather than take the answer already stored.
func editQuery(t *testing.T, e *Executor) {
	t.Helper()
	if err := os.WriteFile(e.Resolver.(*fakeResolver).queries[0], []byte("select 2"), 0o644); err != nil {
		t.Fatalf("editing the query: %v", err)
	}
}

// Without a cache nothing holds the database once it has been analyzed, so the member names no path
// a later run would find empty, and a run that needs it again builds it again.
func TestWithoutACacheABuiltDatabaseIsRemovedAndBuiltAgain(t *testing.T) {
	h, e, id, _ := executorHarness(t)
	delete(h.fetchErr, "octo/absent")

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if path := membersByRepository(t, h.store, id)["octo/absent"].DatabasePath; path != "" {
		t.Errorf("the member names %q, a database nothing retains", path)
	}
	left := filepath.Join(e.WorkDir, fmt.Sprintf("run-%d", id), cacheName("octo/absent"))
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("the member's checkout and database are still at %s: %v", left, err)
	}

	editQuery(t, e)
	built := h.databasesBuilt()
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run again: %v", err)
	}
	if h.databasesBuilt() != built+1 {
		t.Errorf("running again built %d databases, want the one nothing kept", h.databasesBuilt()-built)
	}
}

// A repository nothing has analyzed names no commit, so the build fetches its default branch and
// the member is recorded at the commit that fetch turned out to be.
func TestARepositoryNothingHasAnalyzedIsBuiltAtItsDefaultBranch(t *testing.T) {
	h, e, _, _ := executorHarness(t)
	if _, err := h.store.UpsertRepository("octo/fresh", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	id := runOver(t, h.store, store.AnalysisModeRegular, nil,
		[]store.DatabaseGap{{Repository: "octo/fresh", Reason: "no regular analysis on record"}})

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fresh := membersByRepository(t, h.store, id)["octo/fresh"]
	if fresh.Status != store.AnalysisSucceeded {
		t.Errorf("the member is %q (%s), want %q", fresh.Status, fresh.Detail, store.AnalysisSucceeded)
	}
	if fresh.Commit != "sha-fresh" {
		t.Errorf("the member is recorded at %q, want the commit its default branch fetched", fresh.Commit)
	}
}

// A name nothing tracks is never fetched: ingesting it would enter a stranger's repository into
// the corpus as a seed.
func TestARunNeverFetchesARepositoryABOMDoesNotTrack(t *testing.T) {
	h, e, _, _ := executorHarness(t)
	id := runOver(t, h.store, store.AnalysisModeRegular, nil,
		[]store.DatabaseGap{{Repository: "stranger/repo", Reason: "no regular analysis on record"}})

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := h.fetchCount(); n != 0 {
		t.Errorf("the run fetched %d times for a repository ABOM does not track", n)
	}
	if m := membersByRepository(t, h.store, id)["stranger/repo"]; m.Status != store.AnalysisNoDatabase {
		t.Errorf("the untracked member is %q, want %q", m.Status, store.AnalysisNoDatabase)
	}
	var rows int
	if err := h.store.DB().QueryRow(`
		SELECT COUNT(*) FROM repository WHERE repository_full_name = 'stranger/repo'`).
		Scan(&rows); err != nil || rows != 0 {
		t.Errorf("the run entered the untracked repository into the corpus (%d rows, %v)", rows, err)
	}
}

// Only the crawl's regular shape is built. An extended database built as a regular one would carry
// an identity describing a different artifact.
func TestAnExtendedMemberIsNeverBuiltAsARegularDatabase(t *testing.T) {
	h, e, _, _ := executorHarness(t)
	id := runOver(t, h.store, store.AnalysisModeExtended, []store.ScopedDatabase{{
		DatabaseIdentity: store.DatabaseIdentity{
			Repository: "octo/closure", Commit: absentCommit, SourceFilter: "f0",
			Mode: store.AnalysisModeExtended,
		},
		Status: store.DatabaseBuildable,
	}}, nil)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := h.fetchCount(); n != 0 {
		t.Errorf("the run fetched %d times for an extended database", n)
	}
	if m := membersByRepository(t, h.store, id)["octo/closure"]; m.Status != store.AnalysisNoDatabase || m.Detail == "" {
		t.Errorf("the extended member is %q (%q), want %q with a reason",
			m.Status, m.Detail, store.AnalysisNoDatabase)
	}
}

// A database recorded where nothing is any more is not a database: the member is built again
// rather than analyzed against a path that is gone.
func TestAMemberWhoseDatabaseIsGoneIsBuiltAgain(t *testing.T) {
	h, e, _, _ := executorHarness(t)
	id := runOver(t, h.store, store.AnalysisModeRegular, []store.ScopedDatabase{{
		DatabaseIdentity: store.DatabaseIdentity{
			Repository: "octo/evicted", Commit: absentCommit, SourceFilter: h.expander.sourceFilter(),
			Mode: store.AnalysisModeRegular,
		},
		Status: store.DatabaseRetained, Path: filepath.Join(t.TempDir(), "evicted", "db"),
	}}, nil)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := membersByRepository(t, h.store, id)["octo/evicted"]; m.Status != store.AnalysisSucceeded {
		t.Errorf("the member is %q (%s), want %q", m.Status, m.Detail, store.AnalysisSucceeded)
	}
	if built := h.databasesBuilt(); built != 1 {
		t.Errorf("the builder ran %d times, want once for the database that was gone", built)
	}
}

// Running a run again declines what its first pass answered: the same commit, query version, pack
// and CLI cannot produce a different result, so the member keeps that answer and says where from.
func TestRunningAgainSkipsWhatIsAlreadyAnalyzed(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	first := membersByRepository(t, s, id)["octo-app"]

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run again: %v", err)
	}
	if len(analyzer.targets) != 2 {
		t.Errorf("analyze ran %d times across both passes, want the first pass's 2", len(analyzer.targets))
	}
	by := membersByRepository(t, s, id)
	again := by["octo-app"]
	if again.Status != store.AnalysisSkipped || !strings.Contains(again.Detail, fmt.Sprintf("run %d", id)) {
		t.Errorf("the member is %q (%q), want %q naming the run that answered it",
			again.Status, again.Detail, store.AnalysisSkipped)
	}
	if !again.Counted || again.Results != first.Results ||
		again.ByQuery["actions/unpinned-tag"] != first.ByQuery["actions/unpinned-tag"] {
		t.Errorf("the skipped member reads counted=%v %d %v, want the answer it carries: %d %v",
			again.Counted, again.Results, again.ByQuery, first.Results, first.ByQuery)
	}
	listed, err := s.RunFindings(id, "octo-app", "", 100)
	if err != nil || !listed.Evaluated || len(listed.Findings) != 2 {
		t.Errorf("the skipped member lists %+v (%v), want the 2 findings its answer reported", listed, err)
	}
	if by["octo/absent"].Status != store.AnalysisFailed {
		t.Errorf("the member that could not be built is %q, want it attempted and failed again",
			by["octo/absent"].Status)
	}
}

// A second run over the same databases takes the first run's answer rather than repeating it.
func TestANewRunSkipsWhatAnotherRunAlreadyAnalyzed(t *testing.T) {
	h, e, id, analyzer := executorHarness(t)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held := membersByRepository(t, h.store, id)["octo-app"]
	second := runOver(t, h.store, store.AnalysisModeRegular, []store.ScopedDatabase{{
		DatabaseIdentity: held.DatabaseIdentity, Status: store.DatabaseRetained, Path: held.DatabasePath,
	}}, nil)

	if err := e.Run(context.Background(), second); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(analyzer.targets) != 2 {
		t.Errorf("analyze ran %d times, want only the first run's 2", len(analyzer.targets))
	}
	m := membersByRepository(t, h.store, second)["octo-app"]
	if m.Status != store.AnalysisSkipped || !strings.Contains(m.Detail, fmt.Sprintf("run %d", id)) {
		t.Errorf("the member is %q (%q), want %q naming run %d", m.Status, m.Detail,
			store.AnalysisSkipped, id)
	}
}

// Editing the query changes its version, so the run after an edit analyzes again: a skip can never
// hide a change somebody is tuning.
func TestAnEditedQueryIsAnalyzedAgain(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	editQuery(t, e)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run again: %v", err)
	}
	if len(analyzer.targets) != 4 {
		t.Errorf("analyze ran %d times across both passes, want 4: the edit changed the version",
			len(analyzer.targets))
	}
	if m := membersByRepository(t, s, id)["octo-app"]; m.Status != store.AnalysisSucceeded {
		t.Errorf("the member is %q after an edit, want %q", m.Status, store.AnalysisSucceeded)
	}
}

// A run key names its pack's digest, so a pack that changed between two runs is analyzed again even
// where the query did not. A digest remembered from the first run would answer the second with what
// the pack found as it was.
func TestAPackChangedBetweenRunsIsAnalyzedAgain(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	pack := filepath.Dir(e.Resolver.(*fakeResolver).queries[0])
	if err := os.WriteFile(filepath.Join(pack, "Extra.qll"), []byte("predicate extra() { any() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run again: %v", err)
	}
	if len(analyzer.targets) != 4 {
		t.Errorf("analyze ran %d times across both passes, want 4: the pack changed", len(analyzer.targets))
	}
	want, err := runner.PackDigest(pack)
	if err != nil {
		t.Fatal(err)
	}
	var recorded int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_query_run WHERE pack_digest = ?`, want).
		Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded == 0 {
		t.Error("nothing is recorded against the pack as it is now")
	}
}

// A member with no database whose answer is already stored is not fetched and built only to be
// skipped, and it is recorded at the database that answered it.
func TestAMemberAnsweredElsewhereIsNotBuilt(t *testing.T) {
	h, e, id, _ := executorHarness(t)
	delete(h.fetchErr, "octo/absent")
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	fetched, built := h.fetchCount(), h.databasesBuilt()
	second := runOver(t, h.store, store.AnalysisModeRegular, []store.ScopedDatabase{{
		DatabaseIdentity: store.DatabaseIdentity{
			Repository: "octo/absent", Commit: absentCommit, SourceFilter: "f0",
			Mode: store.AnalysisModeRegular,
		},
		Status: store.DatabaseBuildable,
	}}, nil)

	if err := e.Run(context.Background(), second); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.fetchCount() != fetched || h.databasesBuilt() != built {
		t.Errorf("the second run fetched %d and built %d for an answer already stored",
			h.fetchCount()-fetched, h.databasesBuilt()-built)
	}
	m := membersByRepository(t, h.store, second)["octo/absent"]
	if m.Status != store.AnalysisSkipped {
		t.Errorf("the member is %q (%q), want %q", m.Status, m.Detail, store.AnalysisSkipped)
	}
	if m.SourceFilter != h.expander.sourceFilter() {
		t.Errorf("the member names filter %q, want %q, the database that answered it",
			m.SourceFilter, h.expander.sourceFilter())
	}
}

// cancelingBuilder stops the run while a database is being built, as pressing Stop does.
type cancelingBuilder struct{ cancel context.CancelFunc }

func (c cancelingBuilder) CreateDatabase(ctx context.Context, _, _, _ string) error {
	c.cancel()
	return ctx.Err()
}

// A stop during a build is a stop: the run reads canceled rather than running forever, and the
// member it was building goes back to the state that can be started again.
func TestARunStoppedMidBuildIsCanceledAndItsMemberCanStartAgain(t *testing.T) {
	h, e, id, _ := executorHarness(t)
	delete(h.fetchErr, "octo/absent")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.expander.Builder = cancelingBuilder{cancel}

	if err := e.Run(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("a run stopped mid-build returned %v, want a cancellation", err)
	}
	run, err := h.store.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.State != store.RunCanceled {
		t.Errorf("the run is %q, want %q", run.State, store.RunCanceled)
	}
	if m := membersByRepository(t, h.store, id)["octo/absent"]; m.Status != store.AnalysisPending {
		t.Errorf("the member being built is %q, want %q", m.Status, store.AnalysisPending)
	}
}

// The raw output is the evidence a tuning decision rests on, and re-running to see it again costs
// what the run cost.
func TestASucceededMemberNamesWhereItsResultsWereWritten(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	member := membersByRepository(t, s, id)["octo-app"]
	if member.ResultPath == "" {
		t.Fatal("a succeeded member names no results")
	}
	if _, err := os.Stat(member.ResultPath); err != nil {
		t.Errorf("the recorded result path is not there: %v", err)
	}
}

// How much a version found is the number a reader compares two versions by, and re-reading every
// member's SARIF to get it costs the run again on a page load.
func TestASucceededMemberRecordsHowMuchItFound(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	by := membersByRepository(t, s, id)
	member := by["octo-app"]
	if !member.Counted || member.Results != 2 {
		t.Errorf("an analyzed member reports counted=%v results=%d, want the 2 in the fixture",
			member.Counted, member.Results)
	}
	// A member nothing ran against has no count. Reporting its zero would say a query found
	// nothing in a repository it never looked at.
	if by["octo/absent"].Counted {
		t.Error("a member with no database reports a count")
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.Results != 4 {
		t.Errorf("the run totals %d findings, want 4 across its two analyzed members", run.Results)
	}
}

// The SARIF holds one result from the asked-for rule and one from another: total 2, that query 1.
func TestASucceededMemberSaysWhatEachQueryFound(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := membersByRepository(t, s, id)["octo-app"].ByQuery
	if got["actions/unpinned-tag"] != 1 || got["actions/unnecessary-use-of-advanced-config"] != 1 {
		t.Errorf("the member credits %v, want one result to each rule in the fixture", got)
	}
}

// A result nothing ingested is a result only the run can see: the graph, the Inventory, a policy
// and the gate all read the findings table, so a run that writes SARIF and stops reports numbers
// no other surface can account for.
func TestWhatARunFoundReachesTheTablesEverythingElseReads(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, repo := range []string{"octo-app", "octo-tool"} {
		var n int
		if err := s.DB().QueryRow(`
			SELECT COUNT(*) FROM abom_finding f
			JOIN repository r ON r.row_id = f.repository_row_id
			WHERE r.repository_full_name = ?`, repo).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("%s carries %d findings in the table, want the 2 its SARIF reported", repo, n)
		}
	}
}

// Every stored fact points at a run, so any row can be traced to the query and pack version that
// produced it. A finding attributed to no run is one nobody can re-derive or invalidate.
func TestAnIngestedFindingNamesTheEvaluationThatProducedIt(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var queryID, version, cli string
	if err := s.DB().QueryRow(`
		SELECT q.query_id, COALESCE(q.query_version, ''), COALESCE(q.cli_version, '')
		FROM abom_finding f JOIN abom_query_run q ON q.row_id = f.query_run_id
		LIMIT 1`).Scan(&queryID, &version, &cli); err != nil {
		t.Fatalf("a finding names no run: %v", err)
	}
	if queryID != "actions/unpinned-tag" {
		t.Errorf("the run names %q, want the query the request asked for", queryID)
	}
	if cli == "" {
		t.Error("the run records no CodeQL version, so a later skip cannot tell it apart")
	}
}

// A run of one query records that query's own version, which is what makes it joinable to the
// catalog. A hash of a one-element list is an identity nothing else holds, so the run would
// count against no version and the catalog would report the query as never analyzed.
func TestARunOfOneQueryRecordsThatQuerysOwnVersion(t *testing.T) {
	s, e, id, _ := executorFixture(t)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var recorded string
	if err := s.DB().QueryRow(`
		SELECT COALESCE(query_version, '') FROM abom_query_run
		WHERE query_id = 'actions/unpinned-tag' LIMIT 1`).Scan(&recorded); err != nil {
		t.Fatalf("no run for the query the request named: %v", err)
	}

	want, err := runner.QueryVersion(e.Resolver.(*fakeResolver).queries[0])
	if err != nil {
		t.Fatalf("QueryVersion: %v", err)
	}
	if recorded != want {
		t.Errorf("the run records %q, want the query's own version %q", recorded, want)
	}
}

// A finding no node carries cannot be refused by a gate, drawn on the graph or counted by a unit.
// The crawl attributes at the end of a cycle; a run that skipped it would store results only the
// findings table could see.
func TestARunAttributesWhatItFoundToTheGraph(t *testing.T) {
	s, e, id, _ := executorFixture(t)
	nodeForFinding(t, s, "octo-app")

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var linked int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding_node`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked == 0 {
		t.Error("nothing the run found is attached to a node, so no gate or unit can see it")
	}
}

// A workflow node at the path the fixture SARIF reports, so attribution has something to match.
// The graph is built by the crawl; a run analyzes repositories the crawl has already placed.
func nodeForFinding(t *testing.T, s *store.Store, repository string) {
	t.Helper()
	repoID, err := s.UpsertRepository(repository, "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	// Both fixture results sit in this file, and a workflow node is what the second attribution
	// pass matches on — most rules report on a workflow rather than on a `uses:` line.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_node (node_key, node_kind, repository_row_id, path)
		VALUES (?, 'workflow', ?, '.github/workflows/main.yml')`,
		repository+":workflow", repoID); err != nil {
		t.Fatalf("placing a node: %v", err)
	}
}

// One repository failing is not the run failing. Stopping would discard the work already done and
// leave a reader unable to tell a broken query from a broken repository.
func TestOneMemberFailingDoesNotStopTheRest(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	analyzer.failOn = "octo-app"

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	by := membersByRepository(t, s, id)
	if by["octo-app"].Status != store.AnalysisFailed {
		t.Errorf("the failing member is %q, want %q", by["octo-app"].Status, store.AnalysisFailed)
	}
	if by["octo-app"].Detail == "" {
		t.Error("the failing member does not say what went wrong")
	}
	if by["octo-tool"].Status != store.AnalysisSucceeded {
		t.Errorf("the other member is %q; one failure stopped the run",
			by["octo-tool"].Status)
	}
}

// A run reports complete once nothing is left to attempt, whatever became of its members.
func TestARunCompletesEvenWhenSomeMembersFailed(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	analyzer.failOn = "octo-app"

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if run.State != store.RunComplete {
		t.Errorf("the run is %q, want %q", run.State, store.RunComplete)
	}
	if run.FinishedAt == "" {
		t.Error("a complete run does not say when it finished")
	}
	total := 0
	for _, n := range run.Tally {
		total += n
	}
	if total != len(run.Members) {
		t.Errorf("the tally accounts for %d of %d members", total, len(run.Members))
	}
}

// A query id the configured suites do not resolve to is refused before anything runs. Running the
// rest would report a result for a request that was not carried out.
func TestAQueryTheSuitesDoNotOfferIsRefusedBeforeAnythingRuns(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	if _, err := s.DB().Exec(
		`UPDATE abom_analysis_query SET query_id = ?`, "nobody/writes-this"); err != nil {
		t.Fatalf("renaming the query: %v", err)
	}

	err := e.Run(context.Background(), id)
	if err == nil {
		t.Fatal("a run naming a query nothing offers was carried out")
	}
	if len(analyzer.targets) != 0 {
		t.Errorf("analyze ran %d times for a refused run, want none", len(analyzer.targets))
	}
	run, _ := s.AnalysisRun(id)
	if run.State != store.RunRefused || !strings.Contains(run.Refusal, "nobody/writes-this") {
		t.Errorf("a refused run reads %q %q, want %q naming the query nothing offers",
			run.State, run.Refusal, store.RunRefused)
	}
}

// operatorsQuery points the fixture's run at a query in the pack ABOM scaffolds, and answers the
// pack's directory.
func operatorsQuery(t *testing.T, s *store.Store, e *Executor) string {
	t.Helper()
	query := queryInPack(t, t.TempDir(), customql.Pack, "0.0.1", "CheckoutTagPin.ql", "select 1")
	resolver := e.Resolver.(*fakeResolver)
	resolver.queries = []string{query}
	resolver.meta = map[string]runner.Metadata{query: {ID: "acme/checkout-tag-pin", Kind: "problem"}}
	if _, err := s.DB().Exec(
		`UPDATE abom_analysis_query SET query_id = ?`, "acme/checkout-tag-pin"); err != nil {
		t.Fatalf("renaming the query: %v", err)
	}
	return filepath.Dir(query)
}

// The CLI compiles nothing in a pack whose dependencies were never installed, so a run of the
// operator's query installs them first. Before anything is digested: the lock is part of what a
// pack's digest covers, and a run recorded against the pack as it was before would describe files
// that never ran.
func TestARunOfAnOperatorsQueryInstallsItsPackBeforeRecordingIt(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	pack := operatorsQuery(t, s, e)
	installs := 0
	e.Install = func(context.Context) error {
		installs++
		return os.WriteFile(filepath.Join(pack, "codeql-pack.lock.yml"), []byte("lockVersion: 1.0.0\n"), 0o644)
	}

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if installs != 1 {
		t.Errorf("installed the pack %d times, want once before the run", installs)
	}
	if len(analyzer.targets) == 0 {
		t.Fatal("nothing was analyzed")
	}
	want, err := runner.PackDigest(pack)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB().Query(`SELECT DISTINCT COALESCE(pack_digest, '') FROM abom_query_run WHERE pack = ?`,
		customql.Pack)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var recorded []string
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			t.Fatal(err)
		}
		recorded = append(recorded, digest)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0] != want {
		t.Errorf("the run records the pack as %v, want it as it ran, lock included: %s", recorded, want)
	}
}

// Only the operator's queries need the operator's pack: installing it for a run of shipped ones
// would put a download in front of queries that do not depend on it, and fail them with it.
func TestARunOfShippedQueriesInstallsNothing(t *testing.T) {
	_, e, id, _ := executorFixture(t)
	e.Install = func(context.Context) error {
		t.Error("the operator's pack was installed for a run of shipped queries")
		return nil
	}
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// A pack whose dependencies cannot be installed compiles none of its queries, so the run is refused
// with the CLI's reason before any member is analyzed against it.
func TestARunWhosePackCannotBeInstalledIsRefusedWithWhy(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	operatorsQuery(t, s, e)
	e.Install = func(context.Context) error {
		return errors.New("codeql pack install: exit status 1: Failed to download codeql/actions-all@0.6.2")
	}

	err := e.Run(context.Background(), id)
	if err == nil || !strings.Contains(err.Error(), "Failed to download codeql/actions-all@0.6.2") {
		t.Fatalf("a run whose pack could not be installed returned %v, want the install's reason", err)
	}
	if len(analyzer.targets) != 0 {
		t.Errorf("analyze ran %d times against a pack that could not compile", len(analyzer.targets))
	}
	run, _ := s.AnalysisRun(id)
	if run.State != store.RunRefused || !strings.Contains(run.Refusal, "Failed to download codeql/actions-all@0.6.2") {
		t.Errorf("the run reads %q %q, want %q with the install's reason", run.State, run.Refusal, store.RunRefused)
	}
}

// Stopping a run while it prepares is a stop: nothing refused it, somebody stopped it.
func TestARunStoppedWhileItPreparesIsCanceledRatherThanRefused(t *testing.T) {
	s, e, id, _ := executorFixture(t)
	operatorsQuery(t, s, e)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Install = func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	}

	if err := e.Run(ctx, id); err == nil {
		t.Fatal("a run stopped while it prepared was carried out")
	}
	run, _ := s.AnalysisRun(id)
	if run.State != store.RunCanceled || run.Refusal != "" {
		t.Errorf("the stopped run reads %q %q, want %q with no refusal", run.State, run.Refusal, store.RunCanceled)
	}
}

// Everything a run needs is ready before it is marked running, so one whose directory cannot be made
// is refused, not left running with nothing under way.
func TestARunWhoseDirectoryCannotBeMadeIsRefused(t *testing.T) {
	s, e, id, analyzer := executorFixture(t)
	blocker := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.WorkDir = blocker

	if err := e.Run(context.Background(), id); err == nil {
		t.Fatal("a run with nowhere to write was carried out")
	}
	if len(analyzer.targets) != 0 {
		t.Errorf("analyze ran %d times with nowhere to write", len(analyzer.targets))
	}
	run, _ := s.AnalysisRun(id)
	if run.State != store.RunRefused || !strings.Contains(run.Refusal, blocker) {
		t.Errorf("the run reads %q %q, want %q naming where it could not write", run.State, run.Refusal, store.RunRefused)
	}
}

// Cancellation stops the run rather than carrying on through a corpus somebody has walked away
// from.
func TestACanceledRunStopsRatherThanFinishingTheCorpus(t *testing.T) {
	s, e, id, _ := executorFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := e.Run(ctx, id); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled run returned %v, want a cancellation", err)
	}
	run, _ := s.AnalysisRun(id)
	if run.State != store.RunCanceled {
		t.Errorf("a canceled run is %q, want %q", run.State, store.RunCanceled)
	}
}

// Pushing back on a result needs the result by name, so a member lists what its own evaluation
// reported, by query; one nothing ran against says so rather than listing nothing.
func TestAMemberListsTheFindingsItsEvaluationReported(t *testing.T) {
	s, e, id, _ := executorFixture(t)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, repo := range []string{"octo-app", "octo-tool"} {
		all, err := s.RunFindings(id, repo, "", 100)
		if err != nil {
			t.Fatalf("RunFindings(%s): %v", repo, err)
		}
		if !all.Evaluated || len(all.Findings) != 2 || all.Truncated {
			t.Errorf("%s lists %+v, want the 2 results its SARIF reported", repo, all)
		}
		// Checked against the table rather than trusted: a listing joined on the wrong key reads
		// just as plausibly with another member's results in it.
		for _, f := range all.Findings {
			var owner string
			if err := s.DB().QueryRow(`
				SELECT r.repository_full_name FROM abom_finding f
				JOIN repository r ON r.row_id = f.repository_row_id WHERE f.row_id = ?`,
				f.FindingRowID).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if owner != repo {
				t.Errorf("%s lists finding %d, which %s reported", repo, f.FindingRowID, owner)
			}
		}
	}

	one, err := s.RunFindings(id, "octo-app", "actions/unpinned-tag", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Findings) != 1 || one.Findings[0].RuleID != "actions/unpinned-tag" {
		t.Errorf("narrowed to one query, octo-app lists %+v", one.Findings)
	}

	absent, err := s.RunFindings(id, "octo/absent", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if absent.Evaluated || absent.Findings == nil || len(absent.Findings) != 0 {
		t.Errorf("a member nothing ran against lists %+v, want not evaluated and none", absent)
	}

	capped, err := s.RunFindings(id, "octo-app", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.Findings) != 1 || !capped.Truncated {
		t.Errorf("a listing capped at one reads %+v, want one and truncated", capped)
	}

	if _, err := s.RunFindings(id, "octo/elsewhere", "", 100); !errors.Is(err, store.ErrNoRunMember) {
		t.Errorf("a repository the run does not cover answered %v", err)
	}
}
