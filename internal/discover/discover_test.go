package discover

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type fakeSource struct {
	repos    []string
	withDB   map[string]string
	failOn   string
	lookups  int32
	listErr  error
	listCall int32
}

func (f *fakeSource) ListRepositories(context.Context, string) ([]catalog.Repository, error) {
	atomic.AddInt32(&f.listCall, 1)
	out := make([]catalog.Repository, 0, len(f.repos))
	for _, r := range f.repos {
		out = append(out, catalog.Repository{FullName: r})
	}
	return out, f.listErr
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

func TestMissingLanguageIsRejected(t *testing.T) {
	d := &Discoverer{Source: &fakeSource{}, Store: newStore(t)}
	if _, err := d.RunRepositories(context.Background(), []string{"o/r"}); err == nil {
		t.Error("expected an error when Language is empty")
	}
}

func TestEnumerationFailureIsReturned(t *testing.T) {
	src := &fakeSource{listErr: errors.New("org not found")}
	d := &Discoverer{Source: src, Store: newStore(t), Language: "actions"}

	if _, err := d.Run(context.Background(), "octo-org"); err == nil {
		t.Error("expected the enumeration error to propagate")
	}
}
