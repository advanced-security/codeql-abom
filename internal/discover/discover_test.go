package discover

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type fakeSource struct {
	repos     []string
	withDB    map[string]string
	workflows map[string]*bool
	failOn    string
	lookups   int32
	listErr   error
	listCall  int32
}

func (f *fakeSource) ListAll(context.Context, string) (catalog.Listing, error) {
	atomic.AddInt32(&f.listCall, 1)
	out := make([]catalog.Repository, 0, len(f.repos))
	for _, r := range f.repos {
		out = append(out, catalog.Repository{FullName: r, HasWorkflows: f.workflows[r]})
	}
	return catalog.Listing{Repositories: out}, f.listErr
}

func (f *fakeSource) FindDatabase(_ context.Context, repo, lang string) (catalog.Database, error) {
	atomic.AddInt32(&f.lookups, 1)
	if repo == f.failOn {
		return catalog.Database{}, errors.New("upstream exploded")
	}
	commit, ok := f.withDB[repo]
	if !ok {
		return catalog.Database{}, fmt.Errorf("%s: %w", repo, catalog.ErrNoDatabase)
	}
	return catalog.Database{Repository: repo, Language: lang, CommitOID: commit}, nil
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func TestDiscoverRecordsCatalog(t *testing.T) {
	src := &fakeSource{
		repos:  []string{"octo-org/a", "octo-org/b", "octo-org/c"},
		withDB: map[string]string{"octo-org/a": "commit-a", "octo-org/c": "commit-c"},
	}
	s := newStore(t)
	d := &Discoverer{Source: src, Store: s, Language: "actions", Concurrency: 3}

	stats, err := d.Run(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 3 {
		t.Errorf("Processed = %d, want 3", stats.Processed)
	}

	repos, err := s.CatalogRepositories("actions")
	if err != nil {
		t.Fatalf("CatalogRepositories: %v", err)
	}
	if len(repos) != 2 || repos[0] != "octo-org/a" || repos[1] != "octo-org/c" {
		t.Errorf("catalog = %v, want [octo-org/a octo-org/c]", repos)
	}
}

// Repositories without a database are recorded, not discarded: that record is the CodeQL
// coverage measurement for the organization.
func TestCoverageCountsRepositoriesWithoutDatabases(t *testing.T) {
	src := &fakeSource{
		repos:  []string{"octo-org/a", "octo-org/b", "octo-org/c", "octo-org/d"},
		withDB: map[string]string{"octo-org/a": "commit-a"},
	}
	s := newStore(t)
	d := &Discoverer{Source: src, Store: s, Language: "actions", Concurrency: 2}

	if _, err := d.Run(context.Background(), "octo-org"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	withDB, total, err := s.CatalogCoverage("actions")
	if err != nil {
		t.Fatalf("CatalogCoverage: %v", err)
	}
	if withDB != 1 || total != 4 {
		t.Errorf("coverage = %d/%d, want 1/4", withDB, total)
	}
}

func TestDiscoverIsIdempotent(t *testing.T) {
	src := &fakeSource{
		repos:  []string{"octo-org/a", "octo-org/b"},
		withDB: map[string]string{"octo-org/a": "commit-a"},
	}
	s := newStore(t)
	d := &Discoverer{Source: src, Store: s, Language: "actions"}

	for i := 0; i < 2; i++ {
		if _, err := d.Run(context.Background(), "octo-org"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	_, total, err := s.CatalogCoverage("actions")
	if err != nil {
		t.Fatalf("CatalogCoverage: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d after two runs, want 2", total)
	}
}

// A newly enabled repository must be picked up, and a rebuilt database must update the commit.
func TestRediscoveryUpdatesCommit(t *testing.T) {
	src := &fakeSource{
		repos:  []string{"octo-org/a"},
		withDB: map[string]string{"octo-org/a": "commit-1"},
	}
	s := newStore(t)
	d := &Discoverer{Source: src, Store: s, Language: "actions"}

	if _, err := d.Run(context.Background(), "octo-org"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	src.withDB["octo-org/a"] = "commit-2"
	if _, err := d.Run(context.Background(), "octo-org"); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	commit, ok, err := s.CatalogCommit("octo-org/a", "actions")
	if err != nil {
		t.Fatalf("CatalogCommit: %v", err)
	}
	if !ok || commit != "commit-2" {
		t.Errorf("commit = %q (found=%v), want commit-2", commit, ok)
	}
}

// One unreachable repository must not abandon discovery of the rest.
func TestDiscoverContinuesPastFailures(t *testing.T) {
	src := &fakeSource{
		repos:  []string{"octo-org/a", "octo-org/b", "octo-org/c"},
		withDB: map[string]string{"octo-org/a": "commit-a", "octo-org/c": "commit-c"},
		failOn: "octo-org/b",
	}
	s := newStore(t)
	d := &Discoverer{Source: src, Store: s, Language: "actions", Concurrency: 2}

	stats, err := d.Run(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Failed != 1 || stats.Processed != 2 {
		t.Errorf("Failed = %d Processed = %d, want 1 and 2", stats.Failed, stats.Processed)
	}

	repos, _ := s.CatalogRepositories("actions")
	if len(repos) != 2 {
		t.Errorf("catalog has %d repositories, want 2", len(repos))
	}
}

func TestEveryRepositoryIsProbedOnce(t *testing.T) {
	src := &fakeSource{repos: make([]string, 25), withDB: map[string]string{}}
	for i := range src.repos {
		src.repos[i] = fmt.Sprintf("octo-org/r%d", i)
	}
	s := newStore(t)
	d := &Discoverer{Source: src, Store: s, Language: "actions", Concurrency: 5}

	if _, err := d.Run(context.Background(), "octo-org"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&src.lookups); got != 25 {
		t.Errorf("made %d lookups, want 25", got)
	}
}

func TestActionsDiscoveryLeavesRepositoriesWithoutWorkflowFilesUnprobed(t *testing.T) {
	var probes, listings atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/graphql":
			listings.Add(1)
			fmt.Fprint(writer, `{"data":{"rateLimit":{"cost":1,"remaining":4999,"limit":5000},
				"repositoryOwner":{"repositories":{"pageInfo":{"hasNextPage":false},"nodes":[
				{"nameWithOwner":"octo-org/workflows","defaultBranchRef":{"name":"main"},
				 "workflowTree":{"__typename":"Tree","entries":[{"name":"build.yml","type":"blob"}]}},
				{"nameWithOwner":"octo-org/absent","defaultBranchRef":{"name":"main"},"workflowTree":null},
				{"nameWithOwner":"octo-org/docs","defaultBranchRef":{"name":"main"},
				 "workflowTree":{"__typename":"Tree","entries":[{"name":"README.md","type":"blob"}]}}
				]}}}}`)
		case request.URL.Path == "/orgs/octo-org/repos":
			fmt.Fprint(writer, `[{"full_name":"octo-org/workflows"},
				{"full_name":"octo-org/absent"},{"full_name":"octo-org/docs"}]`)
		case strings.HasSuffix(request.URL.Path, "/code-scanning/codeql/databases"):
			probes.Add(1)
			fmt.Fprint(writer, `[{"language":"actions","commit_oid":"measured-commit"}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	storage := newStore(t)
	discovery := Discoverer{
		Source: catalog.New(server.URL, "", server.Client()), Store: storage, Language: "actions",
	}
	stats, err := discovery.Run(context.Background(), "octo-org")
	if err != nil {
		t.Fatal(err)
	}
	if probes.Load() != 1 || listings.Load() != 1 {
		t.Errorf("database probes = %d, GraphQL listings = %d; want 1 each", probes.Load(), listings.Load())
	}
	if stats.Processed != 1 || stats.Skipped != 2 || stats.Failed != 0 {
		t.Errorf("stats = %+v; want one processed, two unprobed and none failed", stats)
	}
	withDatabase, total, err := storage.CatalogCoverage("actions")
	if err != nil {
		t.Fatal(err)
	}
	if withDatabase != 1 || total != 1 {
		t.Errorf("measured catalog = %d/%d; want 1/1, with skipped repositories unmeasured", withDatabase, total)
	}
}

func TestMissingLanguageIsRejected(t *testing.T) {
	d := &Discoverer{Source: &fakeSource{}, Store: newStore(t)}
	if _, err := d.RunRepositories(context.Background(), []string{"o/r"}); err == nil {
		t.Error("expected an error when Language is empty")
	}
}

func TestProbeCostTracksWorkflowCandidatesNotOrganizationSize(t *testing.T) {
	noWorkflows := false
	source := &fakeSource{repos: make([]string, 20000), workflows: make(map[string]*bool, 19999)}
	for index := range source.repos {
		repository := fmt.Sprintf("octo/r%d", index)
		source.repos[index] = repository
		if index > 0 {
			source.workflows[repository] = &noWorkflows
		}
	}
	discovery := Discoverer{Source: source, Store: newStore(t), Language: "actions"}
	stats, err := discovery.Run(context.Background(), "octo")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 19999 || stats.Processed != 1 || stats.Failed != 0 || source.lookups != 1 {
		t.Fatalf("stats = %+v, probes = %d; want 19999 unprobed and one measured", stats, source.lookups)
	}
}

func TestOtherLanguagesDoNotUseActionsWorkflowEvidence(t *testing.T) {
	noWorkflows := false
	source := &fakeSource{repos: []string{"octo/library"}, workflows: map[string]*bool{"octo/library": &noWorkflows}}
	discovery := Discoverer{Source: source, Store: newStore(t), Language: "javascript"}
	stats, err := discovery.Run(context.Background(), "octo")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Skipped != 0 || stats.Processed != 1 || stats.Failed != 0 || source.lookups != 1 {
		t.Fatalf("stats = %+v, probes = %d; want one probe", stats, source.lookups)
	}
}

func TestSkippingAProbeKeepsEarlierDatabaseEvidence(t *testing.T) {
	source := &fakeSource{repos: []string{"octo/previous"}, withDB: map[string]string{"octo/previous": "earlier-commit"}}
	storage := newStore(t)
	discovery := Discoverer{Source: source, Store: storage, Language: "actions"}
	if _, err := discovery.Run(context.Background(), "octo"); err != nil {
		t.Fatal(err)
	}
	noWorkflows := false
	source.workflows = map[string]*bool{"octo/previous": &noWorkflows}
	stats, err := discovery.Run(context.Background(), "octo")
	if err != nil {
		t.Fatal(err)
	}
	commit, known, err := storage.CatalogCommit("octo/previous", "actions")
	if err != nil || !known || commit != "earlier-commit" || stats.Skipped != 1 || source.lookups != 1 {
		t.Fatalf("commit=%q known=%v error=%v stats=%+v probes=%d", commit, known, err, stats, source.lookups)
	}
}

func TestRESTFallbackDoesNotInventWorkflowAbsence(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/graphql":
			fmt.Fprint(writer, `{"errors":[{"message":"GraphQL is unavailable"}]}`)
		case request.URL.Path == "/orgs/octo/repos":
			fmt.Fprint(writer, `[{"full_name":"octo/first"},{"full_name":"octo/second"}]`)
		case strings.HasSuffix(request.URL.Path, "/code-scanning/codeql/databases"):
			probes.Add(1)
			fmt.Fprint(writer, `[]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	storage := newStore(t)
	discovery := Discoverer{Source: catalog.New(server.URL, "", server.Client()), Store: storage, Language: "actions"}
	stats, err := discovery.Run(context.Background(), "octo")
	if err != nil {
		t.Fatal(err)
	}
	withDatabase, total, err := storage.CatalogCoverage("actions")
	if err != nil || stats.Skipped != 0 || stats.Processed != 2 || probes.Load() != 2 || withDatabase != 0 || total != 2 {
		t.Fatalf("stats=%+v probes=%d catalog=%d/%d error=%v", stats, probes.Load(), withDatabase, total, err)
	}
}

func TestEnumerationFailureIsReturned(t *testing.T) {
	src := &fakeSource{listErr: errors.New("org not found")}
	d := &Discoverer{Source: src, Store: newStore(t), Language: "actions"}

	if _, err := d.Run(context.Background(), "octo-org"); err == nil {
		t.Error("expected the enumeration error to propagate")
	}
}
