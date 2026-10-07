package expand

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// GitHub's code scanning output for an operator's test repository, recorded by regen-codescanning-fixture.sh.
const (
	codeScanningDir      = "../../testdata/codescanning/has-ghas__test-repo-3/"
	codeScanningRepo     = "has-ghas/test-repo-3"
	codeScanningCommit   = "2e5c3a7bd6fde0d96ab38c9c22ad5c16068c883c"
	codeScanningAnalysis = 1894557161
	// codeScanningCLI built that database and evaluated that analysis.
	codeScanningCLI = "2.27.1"
)

// otherCommit is a commit of the same repository that code scanning holds nothing for.
const otherCommit = "e77c401cc58b0000000000000000000000000000"

// fakeCodeScanning serves the recorded database and analysis, and counts what it hands out.
type fakeCodeScanning struct {
	mu sync.Mutex
	// listed is what the database listing says, which a test can make disagree with the database.
	listed      catalog.Database
	listErr     error
	finds       int
	downloadErr error
	downloads   int
	analyses    []catalog.Analysis
	analysesErr error
	sarifErr    error
	sarifs      int
	listings    int
	// onDownload runs while a database download is in flight.
	onDownload func()
	// files answers configuration file reads, keyed "repository|path|ref".
	files   map[string]string
	fileErr error
}

func newFakeCodeScanning() *fakeCodeScanning {
	return &fakeCodeScanning{
		listed: catalog.Database{Repository: codeScanningRepo, Language: "actions", CommitOID: codeScanningCommit},
		// Verbatim from the repository's analysis listing.
		analyses: []catalog.Analysis{{ID: codeScanningAnalysis, CommitSHA: codeScanningCommit,
			Ref: "refs/heads/main", Category: "/language:actions", ToolVersion: codeScanningCLI,
			AnalysisKey: "dynamic/github-code-scanning/codeql:analyze", CreatedAt: "2026-10-05T15:39:46Z"}},
	}
}

func (f *fakeCodeScanning) FindDatabase(context.Context, string, string) (catalog.Database, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finds++
	return f.listed, f.listErr
}

func (f *fakeCodeScanning) DownloadDatabase(_ context.Context, _, _, destPath string) error {
	f.mu.Lock()
	f.downloads++
	err, during := f.downloadErr, f.onDownload
	f.mu.Unlock()
	if during != nil {
		during()
	}
	if err != nil {
		return err
	}
	return copyFixture(codeScanningDir+"database.zip", destPath)
}

func (f *fakeCodeScanning) ActionsAnalyses(context.Context, string, string) ([]catalog.Analysis, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listings++
	return f.analyses, f.analysesErr
}

func (f *fakeCodeScanning) DownloadAnalysis(_ context.Context, _ string, id int64, destPath string) error {
	f.mu.Lock()
	f.sarifs++
	err := f.sarifErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if id != codeScanningAnalysis {
		return fmt.Errorf("asked for analysis %d, which the fixture is not", id)
	}
	return copyFixture(codeScanningDir+"analysis.sarif", destPath)
}

func (f *fakeCodeScanning) FileContent(_ context.Context, repository, path, ref string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fileErr != nil {
		return nil, f.fileErr
	}
	body, ok := f.files[repository+"|"+path+"|"+ref]
	if !ok {
		return nil, &catalog.StatusError{Code: 404}
	}
	return []byte(body), nil
}

// asks counts every request about an analysis: its configuration, the listing and the document.
func (f *fakeCodeScanning) asks() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listings + f.sarifs
}

func (f *fakeCodeScanning) downloaded() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloads
}

func copyFixture(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// suiteResolver resolves each suite to its own queries, under the CodeQL a test names.
type suiteResolver struct {
	suites  map[string][]string
	meta    map[string]runner.Metadata
	version string
}

func (r suiteResolver) ResolveQueries(_ context.Context, suite string) ([]string, error) {
	return r.suites[suite], nil
}

func (r suiteResolver) ResolveMetadata(_ context.Context, path string) (runner.Metadata, error) {
	return r.meta[path], nil
}

func (r suiteResolver) Version(context.Context) (string, error) { return r.version, nil }

// bindSuites binds every tier to suites, in order.
func bindSuites(t *testing.T, h *harness, suites ...string) {
	t.Helper()
	for _, tier := range []string{store.TierOwned, store.TierDirect, store.TierTransitive} {
		if err := h.store.SetTierSuites(tier, suites); err != nil {
			t.Fatalf("SetTierSuites: %v", err)
		}
	}
}

// reuseHarness crawls the fixture's repository at its database's commit, with the graph lane bound under cli.
func reuseHarness(t *testing.T, cli string) (*harness, *fakeCodeScanning) {
	t.Helper()
	h := newHarness(t)
	h.cli = cli
	h.probe.moveEverythingTo(codeScanningCommit)
	edges := queryInPack(t, t.TempDir(), "advanced-security/abom-graph", "1.0.0",
		"AbomEdges.ql", "from int i select i\n")
	h.expander.Resolver = suiteResolver{
		suites:  map[string][]string{"graph.qls": {edges}},
		meta:    map[string]runner.Metadata{edges: {ID: "abom/edges", Tags: ingest.TagEdges}},
		version: cli,
	}
	bindSuites(t, h, "graph.qls")
	cs := newFakeCodeScanning()
	h.expander.CodeScanning = cs
	h.enqueue(item("has-ghas", "test-repo-3", "", 1))
	return h, cs
}

// ownBuildIdentity is the identity ABOM's own build of the fixture's tree would have had.
func ownBuildIdentity(h *harness) store.DatabaseIdentity {
	return store.DatabaseIdentity{
		Repository: codeScanningRepo, Commit: codeScanningCommit,
		SourceFilter: h.expander.sourceFilter(), Mode: store.AnalysisModeRegular,
	}
}

// Code scanning's database of the same commit, CodeQL and files is the one a build would make: taken, nothing built.
func TestCodeScanningsDatabaseOfTheSameTreeStandsInForTheBuild(t *testing.T) {
	h, cs := reuseHarness(t, codeScanningCLI)
	h.retainDatabases()

	stats := runBound(t, h)

	if built := h.databasesBuilt(); built != 0 {
		t.Errorf("built %d databases, want none: code scanning's is the one a build would produce", built)
	}
	if got := h.expander.ReuseCounts(); got.Databases != 1 || len(got.Refused) != 0 {
		t.Errorf("reuse = %+v, want one database taken and nothing refused", got)
	}
	if stats.Edges == 0 || stats.Failed != 0 {
		t.Errorf("edges = %d, failed = %d: the downloaded database was not analyzed", stats.Edges, stats.Failed)
	}
	held, ok, err := h.store.RetainedDatabaseAt(ownBuildIdentity(h))
	if err != nil || !ok {
		t.Fatalf("nothing is retained under the identity ABOM's build would have had: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(held.Path, "codeql-database.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "/home/runner/work/test-repo-3/test-repo-3") {
		t.Errorf("the retained database is not code scanning's:\n%s", body)
	}
	if cs.downloaded() != 1 {
		t.Errorf("downloaded %d times, want once", cs.downloaded())
	}
}

// A round reports what code scanning supplied in it, so the cycle that took a database can say so,
// and the next one does not repeat it.
func TestARoundReportsWhatCodeScanningSuppliedInIt(t *testing.T) {
	h, cs := reuseHarness(t, codeScanningCLI)

	first := runBound(t, h)
	if first.CodeScanning.Databases != 1 || len(first.CodeScanning.Refused) != 0 {
		t.Errorf("first round reports %+v, want the one database it took", first.CodeScanning)
	}
	if got := first.CodeScanning.String(); got != "took 1 database and 0 analyses" {
		t.Errorf("said %q", got)
	}

	cs.listed.CommitOID = otherCommit
	h.probe.moveEverythingTo(otherCommit)
	h.requeueDirect()
	second := runBound(t, h)
	built := h.databasesBuilt()
	if second.CodeScanning.Databases != 0 || built == 0 || second.CodeScanning.Refused["database: another commit"] != built {
		t.Errorf("second round reports %+v and built %d, want only its own refusals, one per build",
			second.CodeScanning, built)
	}
	want := fmt.Sprintf("took 0 databases and 0 analyses; refused database: another commit ×%d", built)
	if got := second.CodeScanning.String(); built > 1 && got != want {
		t.Errorf("said %q, want %q", got, want)
	}
}

// A file the extractor does not index is in no database, so it cannot make code scanning's another.
func TestFilesTheExtractorDoesNotIndexDoNotMakeAnotherDatabase(t *testing.T) {
	h, _ := reuseHarness(t, codeScanningCLI)
	h.files[codeScanningRepo] = map[string]string{
		"README.md":                         "# test-repo-3\n",
		".github/dependabot.yml":            "version: 2\n",
		".github/workflows/nested/ci.yml":   "name: nested\n",
		".github/workflows/notes.txt":       "not a workflow\n",
		".github/reusable_workflows/x.json": "{}\n",
	}

	runBound(t, h)

	if built := h.databasesBuilt(); built != 0 {
		t.Errorf("built %d databases (%+v), want none", built, h.expander.ReuseCounts())
	}
}

// Anything but the database a build would make is built as before, the reason counted, the batch never failed.
func TestADatabaseThatIsNotTheOneABuildWouldProduceIsBuiltAndTheReasonCounted(t *testing.T) {
	for _, c := range []struct {
		name      string
		cli       string
		arrange   func(*harness, *fakeCodeScanning)
		reason    string
		downloads int
	}{
		{name: "code scanning holds none", reason: "no database", arrange: func(_ *harness, cs *fakeCodeScanning) {
			cs.listErr = fmt.Errorf("%s has no actions database: %w", codeScanningRepo, catalog.ErrNoDatabase)
		}},
		{name: "the listing is refused", reason: "not readable", arrange: func(_ *harness, cs *fakeCodeScanning) {
			cs.listErr = &catalog.StatusError{Code: 403, Message: "Resource not accessible by integration"}
		}},
		{name: "the download is refused", reason: "not readable", downloads: 1,
			arrange: func(_ *harness, cs *fakeCodeScanning) {
				cs.downloadErr = &catalog.StatusError{Code: 403, Message: "Resource not accessible by integration"}
			}},
		{name: "the download is gone", reason: "no database", downloads: 1,
			arrange: func(_ *harness, cs *fakeCodeScanning) {
				cs.downloadErr = &catalog.StatusError{Code: 404}
			}},
		// The listing names another commit, so nothing is downloaded to find that out.
		{name: "listed at another commit", reason: "another commit", arrange: func(h *harness, _ *fakeCodeScanning) {
			h.probe.moveEverythingTo(otherCommit)
		}},
		// The listing agrees and the database does not: the database's own word is what counts.
		{name: "built from another commit", reason: "another commit", downloads: 1,
			arrange: func(h *harness, cs *fakeCodeScanning) {
				h.probe.moveEverythingTo(otherCommit)
				cs.listed.CommitOID = otherCommit
			}},
		{name: "built by another CodeQL", cli: "2.27.0", reason: "another CodeQL", downloads: 1},
		{name: "a workflow it did not index", reason: "other files", downloads: 1,
			arrange: func(h *harness, _ *fakeCodeScanning) {
				h.files[codeScanningRepo] = map[string]string{".github/workflows/release.yml": "name: release\n"}
			}},
		{name: "an action it did not index", reason: "other files", downloads: 1,
			arrange: func(h *harness, _ *fakeCodeScanning) {
				h.files[codeScanningRepo] = map[string]string{"deploy/action.yaml": "name: deploy\n"}
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cli := c.cli
			if cli == "" {
				cli = codeScanningCLI
			}
			h, cs := reuseHarness(t, cli)
			if c.arrange != nil {
				c.arrange(h, cs)
			}

			stats := runBound(t, h)

			if built := h.databasesBuilt(); built != 1 {
				t.Errorf("built %d databases, want the one build there would have been", built)
			}
			got := h.expander.ReuseCounts()
			if got.Databases != 0 || got.Refused["database: "+c.reason] != 1 || len(got.Refused) != 1 {
				t.Errorf("reuse = %+v, want only database: %s", got, c.reason)
			}
			if stats.Failed != 0 || stats.Edges == 0 {
				t.Errorf("failed = %d, edges = %d: a refused download has to cost the build and nothing else",
					stats.Failed, stats.Edges)
			}
			if cs.downloaded() != c.downloads {
				t.Errorf("downloaded %d times, want %d", cs.downloaded(), c.downloads)
			}
		})
	}
}

// Without a source the crawl builds everything, as it did before code scanning was asked at all.
func TestWithoutCodeScanningEveryDatabaseIsBuilt(t *testing.T) {
	h, _ := reuseHarness(t, codeScanningCLI)
	h.expander.CodeScanning = nil

	runBound(t, h)

	if built := h.databasesBuilt(); built != 1 {
		t.Errorf("built %d databases, want 1", built)
	}
}

// A round that does not know its CodeQL can match no database, so asking would spend a download on nothing.
func TestARoundThatDoesNotKnowItsCodeQLDoesNotAsk(t *testing.T) {
	h, cs := reuseHarness(t, "")

	runBound(t, h)

	if cs.finds != 0 || cs.downloaded() != 0 {
		t.Errorf("listed %d and downloaded %d times for a round that cannot accept either", cs.finds, cs.downloaded())
	}
	if got := h.expander.ReuseCounts(); len(got.Refused) != 0 || h.databasesBuilt() != 1 {
		t.Errorf("reuse = %+v and built %d, want nothing asked and the build", got, h.databasesBuilt())
	}
}

// Index filters in the server's environment reach every build ABOM runs and none of GitHub's, so
// no download can be the database ABOM would build.
func TestIndexFiltersInTheEnvironmentMeanNothingIsAsked(t *testing.T) {
	t.Setenv("LGTM_INDEX_FILTERS", "include:docs/**/*.yml")
	h, cs := reuseHarness(t, codeScanningCLI)

	runBound(t, h)

	if cs.finds != 0 || cs.downloaded() != 0 || h.databasesBuilt() != 1 {
		t.Errorf("listed %d, downloaded %d and built %d; want nothing asked and the build",
			cs.finds, cs.downloaded(), h.databasesBuilt())
	}
}

// A run builds a missing database through the crawl's own step, so it takes code scanning's on the same terms.
func TestARunTakesCodeScanningsDatabaseInsteadOfBuildingIt(t *testing.T) {
	h, _ := reuseHarness(t, codeScanningCLI)
	h.retainDatabases()

	built, err := h.expander.buildDatabase(context.Background(), store.DatabaseIdentity{
		Repository: codeScanningRepo, Commit: codeScanningCommit, Mode: store.AnalysisModeRegular,
	}, filepath.Join(t.TempDir(), "member"), codeScanningCLI)
	if err != nil {
		t.Fatalf("buildDatabase: %v", err)
	}
	defer built.release()

	if n := h.databasesBuilt(); n != 0 {
		t.Errorf("built %d databases, want code scanning's taken instead", n)
	}
	if built.identity != ownBuildIdentity(h) || built.kept == "" {
		t.Errorf("got %+v kept at %q, want it retained under %+v", built.identity, built.kept, ownBuildIdentity(h))
	}
	if _, ok, err := h.store.RetainedDatabaseAt(ownBuildIdentity(h)); err != nil || !ok {
		t.Errorf("the run's database is not on record: %v", err)
	}
}

// A round stopped mid-download is not a refusal, and must not be counted as one.
func TestACanceledDownloadIsNotCountedAsARefusal(t *testing.T) {
	h, cs := reuseHarness(t, codeScanningCLI)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cs.onDownload = cancel
	cs.downloadErr = context.Canceled

	built, err := h.expander.buildDatabase(ctx, ownBuildIdentity(h), t.TempDir(), codeScanningCLI)
	if err == nil {
		built.release()
	}
	if cs.downloaded() != 1 {
		t.Fatalf("downloaded %d times, want the one that was canceled", cs.downloaded())
	}
	if got := h.expander.ReuseCounts(); len(got.Refused) != 0 || got.Databases != 0 {
		t.Errorf("reuse = %+v, want a canceled download counted as nothing", got)
	}
}
