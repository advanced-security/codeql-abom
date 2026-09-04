package pipeline

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

type fakeSource struct {
	commit    string
	notFound  bool
	downloads int32
	lookups   int32
}

func (f *fakeSource) FindDatabase(_ context.Context, repo, lang string) (catalog.Database, error) {
	atomic.AddInt32(&f.lookups, 1)
	if f.notFound {
		return catalog.Database{}, fmt.Errorf("%s: %w", repo, catalog.ErrNoDatabase)
	}
	return catalog.Database{Repository: repo, Language: lang, CommitOID: f.commit}, nil
}

func (f *fakeSource) DownloadDatabase(_ context.Context, _, _, destPath string) error {
	atomic.AddInt32(&f.downloads, 1)
	return writeDatabaseZip(destPath)
}

type fakeRunner struct{ runs int32 }

// writeDatabaseZip produces the minimum archive runner.Extract accepts: a database marker.
func writeDatabaseZip(destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create("actions-database/codeql-database.yml")
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("sourceLocationPrefix: /src\n")); err != nil {
		return err
	}
	return zw.Close()
}

func (f *fakeRunner) RunQuery(_ context.Context, _, _, outputPath string) error {
	atomic.AddInt32(&f.runs, 1)
	return os.WriteFile(outputPath, []byte("bqrs"), 0o644)
}

// fixtureDecoder substitutes the real query output, so the pipeline can be exercised without
// the CodeQL CLI.
func fixtureDecoder(t *testing.T) Decoder {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "bqrs", "testdata", "abom-edges.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return func(_ context.Context, _, jsonPath string) error {
		return os.WriteFile(jsonPath, data, 0o644)
	}
}

func newPipeline(t *testing.T, src DatabaseSource, r QueryRunner) (*Pipeline, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	w := store.NewWriter(s, 0)
	return &Pipeline{
		Source:   src,
		Runner:   r,
		Decode:   fixtureDecoder(t),
		Store:    s,
		Ingester: ingest.New(s, w),
		Writer:   w,
		Config: Config{
			Language:         "actions",
			QueryPath:        "/queries/AbomEdges.ql",
			QueryID:          "actions/abom/edges",
			WorkDir:          t.TempDir(),
			Concurrency:      2,
			ExtractorVersion: "test",
		},
	}, s
}

func TestPipelineIngestsRepositories(t *testing.T) {
	src := &fakeSource{commit: "abc123"}
	p, s := newPipeline(t, src, &fakeRunner{})

	stats, err := p.Run(context.Background(), []string{"octo-org/a", "octo-org/b"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 2 {
		t.Errorf("Processed = %d, want 2", stats.Processed)
	}
	if stats.Rows != 26 {
		t.Errorf("Rows = %d, want 26 (13 edges x 2 repositories)", stats.Rows)
	}

	var repos int
	s.DB().QueryRow(`SELECT COUNT(*) FROM repository`).Scan(&repos)
	if repos != 2 {
		t.Errorf("stored %d repositories, want 2", repos)
	}
}

// The whole point of reading commit metadata first: an unchanged repository must cost a
// metadata lookup and nothing more.
func TestUnchangedRepositoryIsNotDownloadedAgain(t *testing.T) {
	src := &fakeSource{commit: "abc123"}
	p, _ := newPipeline(t, src, &fakeRunner{})

	if _, err := p.Run(context.Background(), []string{"octo-org/a"}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if got := atomic.LoadInt32(&src.downloads); got != 1 {
		t.Fatalf("first run made %d downloads, want 1", got)
	}

	stats, err := p.Run(context.Background(), []string{"octo-org/a"})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if stats.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", stats.Skipped)
	}
	if got := atomic.LoadInt32(&src.downloads); got != 1 {
		t.Errorf("second run downloaded again (%d total)", got)
	}
	if got := atomic.LoadInt32(&src.lookups); got != 2 {
		t.Errorf("lookups = %d, want 2", got)
	}
}

// A new commit invalidates the ledger entry and the work runs again.
func TestNewCommitIsReprocessed(t *testing.T) {
	src := &fakeSource{commit: "commit1"}
	p, _ := newPipeline(t, src, &fakeRunner{})

	if _, err := p.Run(context.Background(), []string{"octo-org/a"}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	src.commit = "commit2"
	stats, err := p.Run(context.Background(), []string{"octo-org/a"})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if stats.Processed != 1 {
		t.Errorf("Processed = %d, want 1", stats.Processed)
	}
	if got := atomic.LoadInt32(&src.downloads); got != 2 {
		t.Errorf("downloads = %d, want 2", got)
	}
}

// Most repositories in a large organization have no actions database.
func TestRepositoriesWithoutDatabaseAreSkipped(t *testing.T) {
	src := &fakeSource{notFound: true}
	p, _ := newPipeline(t, src, &fakeRunner{})

	stats, err := p.Run(context.Background(), []string{"octo-org/a", "octo-org/b"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Skipped != 2 || stats.Failed != 0 {
		t.Errorf("Skipped = %d Failed = %d, want 2 and 0", stats.Skipped, stats.Failed)
	}
	if got := atomic.LoadInt32(&src.downloads); got != 0 {
		t.Errorf("downloaded %d databases for repositories with none", got)
	}
}

// Artifacts must not accumulate across an organization.
func TestArtifactsAreRemovedAfterIngestion(t *testing.T) {
	src := &fakeSource{commit: "abc123"}
	p, _ := newPipeline(t, src, &fakeRunner{})

	if _, err := p.Run(context.Background(), []string{"octo-org/a"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries, err := os.ReadDir(p.Config.WorkDir)
	if err != nil {
		t.Fatalf("reading work dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("work directory still holds %d entries", len(entries))
	}
}

func TestKeepArtifactsRetainsWorkDirectory(t *testing.T) {
	src := &fakeSource{commit: "abc123"}
	p, _ := newPipeline(t, src, &fakeRunner{})
	p.Config.KeepArtifacts = true

	if _, err := p.Run(context.Background(), []string{"octo-org/a"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	entries, err := os.ReadDir(p.Config.WorkDir)
	if err != nil {
		t.Fatalf("reading work dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("KeepArtifacts did not retain the work directory")
	}
}

// A failed unit must be recorded as failed so the next run retries it.
func TestFailedUnitIsRetriedOnNextRun(t *testing.T) {
	src := &fakeSource{commit: "abc123"}
	p, s := newPipeline(t, src, &fakeRunner{})

	var failing atomic.Bool
	failing.Store(true)
	good := p.Decode
	p.Decode = func(ctx context.Context, b, j string) error {
		if failing.Load() {
			return fmt.Errorf("decode exploded")
		}
		return good(ctx, b, j)
	}

	stats, err := p.Run(context.Background(), []string{"octo-org/a"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Failed != 1 {
		t.Fatalf("Failed = %d, want 1", stats.Failed)
	}

	failing.Store(false)
	stats, err = p.Run(context.Background(), []string{"octo-org/a"})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if stats.Processed != 1 {
		t.Errorf("Processed = %d, want 1 after retry", stats.Processed)
	}

	counts, err := s.LedgerCounts()
	if err != nil {
		t.Fatalf("LedgerCounts: %v", err)
	}
	if counts[store.StatusDone] != 1 {
		t.Errorf("done count = %d, want 1", counts[store.StatusDone])
	}
}

func TestMissingConfigIsRejected(t *testing.T) {
	p, _ := newPipeline(t, &fakeSource{}, &fakeRunner{})
	p.Config.WorkDir = ""
	if _, err := p.Run(context.Background(), []string{"o/r"}); err == nil {
		t.Error("expected an error when WorkDir is empty")
	}
}

func TestEventsAreReported(t *testing.T) {
	src := &fakeSource{commit: "abc123"}
	p, _ := newPipeline(t, src, &fakeRunner{})

	var done int32
	p.OnEvent = func(e work.Event) {
		if e.Kind == work.EventDone {
			atomic.AddInt32(&done, 1)
		}
	}
	if _, err := p.Run(context.Background(), []string{"octo-org/a", "octo-org/b"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&done); got != 2 {
		t.Errorf("done events = %d, want 2", got)
	}
}
