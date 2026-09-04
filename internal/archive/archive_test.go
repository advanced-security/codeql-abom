package archive

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/store"
)

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

func writeArchive(t *testing.T, dir, name string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create("db/codeql-database.yml")
	if err != nil {
		t.Fatalf("adding marker: %v", err)
	}
	w.Write([]byte("sourceLocationPrefix: /src\n"))
	if err := zw.Close(); err != nil {
		t.Fatalf("closing zip: %v", err)
	}
}

func writeSarifWithProvenance(t *testing.T, dir, name, repo string) {
	t.Helper()
	body := `{"runs":[{"versionControlProvenance":[
		{"repositoryUri":"https://github.com/` + repo + `","revisionId":"abc123"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func newRunner(t *testing.T, s *store.Store, edges string) (*Runner, *int32) {
	t.Helper()
	var runs int32
	w := store.NewWriter(s, 0)
	return &Runner{
		CodeQL: runnerFunc(func(ctx context.Context, dbPath, query, out string) error {
			atomic.AddInt32(&runs, 1)
			return os.WriteFile(out, []byte("bqrs"), 0o644)
		}),
		Extract: func(zipPath, destDir string) (string, error) {
			if err := os.MkdirAll(destDir, 0o755); err != nil {
				return "", err
			}
			return destDir, nil
		},
		Decode: func(_ context.Context, _, jsonPath string) error {
			return os.WriteFile(jsonPath, []byte(edges), 0o644)
		},
		Store:    s,
		Ingester: ingest.New(s, w),
		Writer:   w,
	}, &runs
}

type runnerFunc func(context.Context, string, string, string) error

func (f runnerFunc) RunQuery(ctx context.Context, dbPath, query, out string) error {
	return f(ctx, dbPath, query, out)
}

const edgesJSON = `{"#select":{"columns":[
	{"name":"edgeKind","kind":"String"},{"name":"srcKind","kind":"String"},
	{"name":"srcPath","kind":"String"},{"name":"srcJobId","kind":"String"},
	{"name":"srcStepIndex","kind":"Integer"},{"name":"srcStepId","kind":"String"},
	{"name":"usesRaw","kind":"String"},{"name":"pinComment","kind":"String"},
	{"name":"startLine","kind":"Integer"},{"name":"startColumn","kind":"Integer"},
	{"name":"endLine","kind":"Integer"},{"name":"endColumn","kind":"Integer"}],
	"tuples":[["step-uses-action","job",".github/workflows/ci.yml","build",0,"",
	 "actions/checkout@v4","",9,15,9,33]]}}`

func TestRunIngestsEveryArchive(t *testing.T) {
	dir := t.TempDir()
	writeArchive(t, dir, "octo-org_app_1_db.zip")
	writeSarifWithProvenance(t, dir, "octo-org_app_1.sarif", "octo-org/app")
	writeArchive(t, dir, "octo-org_service_1_db.zip")
	writeSarifWithProvenance(t, dir, "octo-org_service_1.sarif", "octo-org/service")

	s := newStore(t)
	r, runs := newRunner(t, s, edgesJSON)

	stats, err := r.Run(context.Background(), Options{
		Dir: dir, QueryPath: "/q.ql", QueryID: "abom/edges",
		WorkDir: t.TempDir(), Concurrency: 2,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 2 {
		t.Errorf("Processed = %d, want 2", stats.Processed)
	}
	if stats.Rows != 2 {
		t.Errorf("Rows = %d, want 2", stats.Rows)
	}
	if n := atomic.LoadInt32(runs); n != 2 {
		t.Errorf("ran the query %d times, want 2", n)
	}

	var repos int
	s.DB().QueryRow(`SELECT COUNT(*) FROM repository`).Scan(&repos)
	if repos != 2 {
		t.Errorf("recorded %d repositories, want 2", repos)
	}
}

// Provenance from the sibling SARIF is authoritative; the filename is only a fallback.
func TestRunPrefersSiblingProvenance(t *testing.T) {
	dir := t.TempDir()
	// The archive name says one thing, the run records another. The run wins.
	writeArchive(t, dir, "wrong-org_wrong-name_1_db.zip")
	writeSarifWithProvenance(t, dir, "wrong-org_wrong-name_1.sarif", "real-org/real-name")

	s := newStore(t)
	r, _ := newRunner(t, s, edgesJSON)

	if _, err := r.Run(context.Background(), Options{
		Dir: dir, QueryPath: "/q.ql", QueryID: "abom/edges", WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var name string
	if err := s.DB().QueryRow(`SELECT repository_full_name FROM repository`).Scan(&name); err != nil {
		t.Fatalf("reading repository: %v", err)
	}
	if name != "real-org/real-name" {
		t.Errorf("attributed to %q, want real-org/real-name", name)
	}
}

func TestRunFallsBackToFilename(t *testing.T) {
	dir := t.TempDir()
	writeArchive(t, dir, "octo-org_app_1_db.zip")

	s := newStore(t)
	r, _ := newRunner(t, s, edgesJSON)

	stats, err := r.Run(context.Background(), Options{
		Dir: dir, QueryPath: "/q.ql", QueryID: "abom/edges", WorkDir: t.TempDir(),
		Resolver: ingest.NewRepoResolver([]string{"octo-org/app"}),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 1 {
		t.Errorf("Processed = %d, want 1", stats.Processed)
	}
}

// Extracted databases dominate disk use, so they must not survive the run.
func TestRunRemovesExtractedDatabases(t *testing.T) {
	dir := t.TempDir()
	writeArchive(t, dir, "octo-org_app_1_db.zip")
	writeSarifWithProvenance(t, dir, "octo-org_app_1.sarif", "octo-org/app")

	workDir := t.TempDir()
	s := newStore(t)
	r, _ := newRunner(t, s, edgesJSON)

	if _, err := r.Run(context.Background(), Options{
		Dir: dir, QueryPath: "/q.ql", QueryID: "abom/edges", WorkDir: workDir,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("reading work dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("work directory still holds %d entries", len(entries))
	}
}

// One unreadable archive must not abandon the rest of the run.
func TestRunContinuesPastFailures(t *testing.T) {
	dir := t.TempDir()
	for _, repo := range []string{"app", "service", "tool"} {
		writeArchive(t, dir, "octo-org_"+repo+"_1_db.zip")
		writeSarifWithProvenance(t, dir, "octo-org_"+repo+"_1.sarif", "octo-org/"+repo)
	}

	s := newStore(t)
	r, _ := newRunner(t, s, edgesJSON)
	r.CodeQL = runnerFunc(func(ctx context.Context, dbPath, query, out string) error {
		if filepath.Base(filepath.Dir(out)) == "octo-org_service" {
			return errors.New("evaluator exploded")
		}
		return os.WriteFile(out, []byte("bqrs"), 0o644)
	})

	stats, err := r.Run(context.Background(), Options{
		Dir: dir, QueryPath: "/q.ql", QueryID: "abom/edges", WorkDir: t.TempDir(),
		Concurrency: 2,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 2 || stats.Failed != 1 {
		t.Errorf("Processed = %d Failed = %d, want 2 and 1", stats.Processed, stats.Failed)
	}
}

func TestRunIgnoresNonArchives(t *testing.T) {
	dir := t.TempDir()
	writeArchive(t, dir, "octo-org_app_1_db.zip")
	writeSarifWithProvenance(t, dir, "octo-org_app_1.sarif", "octo-org/app")
	os.WriteFile(filepath.Join(dir, "octo-org_app_1.bqrs"), []byte("binary"), 0o644)

	s := newStore(t)
	r, _ := newRunner(t, s, edgesJSON)

	stats, err := r.Run(context.Background(), Options{
		Dir: dir, QueryPath: "/q.ql", QueryID: "abom/edges", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Processed != 1 {
		t.Errorf("Processed = %d, want 1", stats.Processed)
	}
}

func TestRunRequiresQueryAndWorkDir(t *testing.T) {
	s := newStore(t)
	r, _ := newRunner(t, s, edgesJSON)

	if _, err := r.Run(context.Background(), Options{Dir: t.TempDir()}); err == nil {
		t.Error("expected an error when QueryPath and WorkDir are missing")
	}
}
