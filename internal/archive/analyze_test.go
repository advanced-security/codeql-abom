package archive

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// findingsSARIF is real `codeql database analyze --format=sarif-latest` output. Regenerate with:
//
//	codeql database create --language=actions \
//	  --source-root=ql/actions/abom/test/AbomEdges /tmp/abom-fixture-db
//	codeql database analyze /tmp/abom-fixture-db \
//	  codeql/actions-queries:codeql-suites/actions-all.qls \
//	  --format=sarif-latest --output=internal/archive/testdata/findings.sarif
const findingsSARIF = "testdata/findings.sarif"

type analyzerFunc func(ctx context.Context, dbPath, suite, sarifPath string) error

func (f analyzerFunc) AnalyzeDatabase(ctx context.Context, dbPath, suite, sarifPath string) error {
	return f(ctx, dbPath, suite, sarifPath)
}

// newAnalyzer wires a Runner whose suite evaluation serves the recorded SARIF.
func newAnalyzer(t *testing.T, s *store.Store) (*Runner, *int32) {
	t.Helper()
	body, err := os.ReadFile(findingsSARIF)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	var runs int32
	w := store.NewWriter(s, 0)
	return &Runner{
		Suite: analyzerFunc(func(_ context.Context, _, _, sarifPath string) error {
			atomic.AddInt32(&runs, 1)
			return os.WriteFile(sarifPath, body, 0o644)
		}),
		Extract: func(zipPath, destDir string) (string, error) {
			if err := os.MkdirAll(destDir, 0o755); err != nil {
				return "", err
			}
			return destDir, nil
		},
		Store:    s,
		Ingester: ingest.New(s, w),
		Writer:   w,
	}, &runs
}

func analyzeDir(t *testing.T, repos ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, repo := range repos {
		name := "results_" + repo[:len(repo)-len(filepath.Base(repo))-1] + "_" + filepath.Base(repo)
		writeArchive(t, dir, name+"_db.zip")
		writeSarifWithProvenance(t, dir, name+".sarif", repo)
	}
	return dir
}

func TestAnalyzeIngestsFindingsFromEveryArchive(t *testing.T) {
	s := newStore(t)
	r, runs := newAnalyzer(t, s)
	dir := analyzeDir(t, "octo-org/app", "octo-org/service")

	stats, err := r.Analyze(context.Background(), AnalyzeOptions{
		Dir: dir, Suite: "actions-all.qls", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if got := atomic.LoadInt32(runs); got != 2 {
		t.Errorf("evaluated %d databases, want 2", got)
	}
	if stats.Processed != 2 {
		t.Errorf("Processed = %d, want 2", stats.Processed)
	}

	var findings int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding`).Scan(&findings)
	if findings == 0 {
		t.Error("no findings ingested")
	}
}

// The suite is the point: a run that collapsed every result onto one rule would lose the
// distinction the posture report is built on.
func TestAnalyzeRecordsEveryRule(t *testing.T) {
	s := newStore(t)
	r, _ := newAnalyzer(t, s)

	if _, err := r.Analyze(context.Background(), AnalyzeOptions{
		Dir: analyzeDir(t, "octo-org/app"), Suite: "actions-all.qls", WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	rows, err := s.DB().Query(`SELECT DISTINCT rule_id FROM abom_finding ORDER BY rule_id`)
	if err != nil {
		t.Fatalf("reading rules: %v", err)
	}
	defer rows.Close()

	var rules []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		rules = append(rules, id)
	}
	if len(rules) < 2 {
		t.Errorf("ingested rules = %v, want the fixture's distinct rules", rules)
	}
}

func TestAnalyzeAttributesFindingsToTheirRepository(t *testing.T) {
	s := newStore(t)
	r, _ := newAnalyzer(t, s)

	if _, err := r.Analyze(context.Background(), AnalyzeOptions{
		Dir: analyzeDir(t, "octo-org/app"), Suite: "actions-all.qls", WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	var repo string
	if err := s.DB().QueryRow(`
		SELECT r.repository_full_name FROM abom_finding f
		JOIN repository r ON r.row_id = f.repository_row_id LIMIT 1`).Scan(&repo); err != nil {
		t.Fatalf("reading attribution: %v", err)
	}
	if repo != "octo-org/app" {
		t.Errorf("attributed to %q", repo)
	}
}

// Each analysis has to be traceable to the suite that produced it.
func TestAnalyzeRecordsProvenance(t *testing.T) {
	s := newStore(t)
	r, _ := newAnalyzer(t, s)

	if _, err := r.Analyze(context.Background(), AnalyzeOptions{
		Dir: analyzeDir(t, "octo-org/app"), Suite: "actions-all.qls", WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	var queryID string
	if err := s.DB().QueryRow(
		`SELECT query_id FROM abom_query_run WHERE repository = 'octo-org/app'`).
		Scan(&queryID); err != nil {
		t.Fatalf("reading provenance: %v", err)
	}
	if queryID != "actions-all.qls" {
		t.Errorf("query_id = %q, want the suite", queryID)
	}
}

// Databases are the bulk of the disk cost and are finished with once the suite has run.
func TestAnalyzeRemovesExtractedDatabases(t *testing.T) {
	s := newStore(t)
	r, _ := newAnalyzer(t, s)
	workDir := t.TempDir()

	if _, err := r.Analyze(context.Background(), AnalyzeOptions{
		Dir: analyzeDir(t, "octo-org/app"), Suite: "actions-all.qls", WorkDir: workDir,
	}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatalf("reading work directory: %v", err)
	}
	for _, e := range entries {
		db := filepath.Join(workDir, e.Name(), "db")
		if _, err := os.Stat(db); err == nil {
			t.Errorf("%s survived the run", db)
		}
	}
}

// One unanalysable repository must not end a run covering thousands.
func TestAnalyzeFailureDoesNotAbortTheRun(t *testing.T) {
	s := newStore(t)
	r, _ := newAnalyzer(t, s)
	dir := analyzeDir(t, "octo-org/app", "octo-org/service")

	var events []work.Event
	r.OnEvent = func(e work.Event) { events = append(events, e) }
	r.Suite = analyzerFunc(func(_ context.Context, _, _, sarifPath string) error {
		if filepath.Base(filepath.Dir(sarifPath)) == "octo-org_app" {
			return os.ErrPermission
		}
		body, _ := os.ReadFile(findingsSARIF)
		return os.WriteFile(sarifPath, body, 0o644)
	})

	stats, err := r.Analyze(context.Background(), AnalyzeOptions{
		Dir: dir, Suite: "actions-all.qls", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if stats.Processed != 1 || stats.Failed != 1 {
		t.Errorf("stats = %+v, want one processed and one failed", stats)
	}
}

func TestAnalyzeRequiresSuiteAndWorkDir(t *testing.T) {
	s := newStore(t)
	r, _ := newAnalyzer(t, s)

	if _, err := r.Analyze(context.Background(), AnalyzeOptions{Dir: t.TempDir()}); err == nil {
		t.Error("Analyze accepted a run with no suite or work directory")
	}
}
