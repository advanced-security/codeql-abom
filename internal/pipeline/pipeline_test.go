package pipeline

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// abomDatabaseEnv points at a prebuilt CodeQL actions database. Building one takes far longer
// than a unit test should, so the integration test runs only when a database is supplied.
// `make itest` builds it and sets this.
const abomDatabaseEnv = "ABOM_ITEST_DB"

// Exercises the real chain: CodeQL CLI evaluates the query, BQRS is streamed and classified,
// rows land in SQLite, and the graph answers a blast-radius question.
func TestEndToEndQueryToGraph(t *testing.T) {
	dbPath := os.Getenv(abomDatabaseEnv)
	if dbPath == "" {
		t.Skipf("set %s to a prebuilt actions database to run this test", abomDatabaseEnv)
	}
	// Setting the variable is the opt-in, so a missing CLI past this point is a broken
	// environment, not a reason to report success having run nothing.
	if _, err := exec.LookPath("codeql"); err != nil {
		t.Fatalf("%s is set, so this test was asked for, but codeql is not on PATH: %v",
			abomDatabaseEnv, err)
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	queryPath := filepath.Join(repoRoot, "ql", "actions", "graph-extraction", "src", "AbomEdges.ql")

	bqrsPath := filepath.Join(t.TempDir(), "edges.bqrs")
	r := &runner.Runner{}
	if err := r.RunQuery(context.Background(), dbPath, queryPath, bqrsPath); err != nil {
		t.Fatalf("RunQuery: %v", err)
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	w := store.NewWriter(s, 0)
	ing := ingest.New(s, w)

	f, err := os.Open(bqrsPathJSON(t, bqrsPath))
	if err != nil {
		t.Fatalf("opening decoded bqrs: %v", err)
	}
	defer f.Close()

	run := store.QueryRun{
		QueryID: "abom/edges", Repository: "octo-org/app", AnalysisMode: store.AnalysisModeRegular,
	}
	regular, err := s.RecordQueryRun(run)
	if err != nil {
		t.Fatal(err)
	}
	n, err := ing.EdgesFromBQRS(f, ingest.Options{Repository: "octo-org/app", QueryRunID: regular})
	if err != nil {
		t.Fatalf("EdgesFromBQRS: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}

	// By kind rather than as a total. This read 13 for as long as the test was skipped, and the
	// three that arrived are `job-needs-job` — ordering, not use — which a bare count would have
	// been bumped past without anyone noticing which kind moved.
	want := map[string]int{
		"step-uses-action":           10,
		"job-uses-reusable-workflow": 3,
		"composite-step-uses-action": 2,
		"job-needs-job":              3,
	}
	total := 0
	for kind, expected := range want {
		var got int
		if err := s.DB().QueryRow(
			`SELECT COUNT(*) FROM abom_edge WHERE edge_kind = ?`, kind).Scan(&got); err != nil {
			t.Fatalf("counting %s: %v", kind, err)
		}
		if got != expected {
			t.Errorf("%s edges = %d, want %d", kind, got, expected)
		}
		total += expected
	}
	if n != total {
		t.Errorf("ingested %d edges, want %d across the kinds above", n, total)
	}

	// actions/checkout is used by both workflows in the fixture.
	var uses int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_edge e
		JOIN abom_node n ON n.row_id = e.dst_node_row_id
		WHERE n.node_key = 'actions/checkout'`).Scan(&uses); err != nil {
		t.Fatalf("counting checkout uses: %v", err)
	}
	if uses != 2 {
		t.Errorf("actions/checkout used by %d edges, want 2", uses)
	}

	// Pin comments are only recoverable from QL, so their survival through the real chain is
	// what this asserts. Whether the reference is pinned is a query's verdict, not ABOM's.
	var withComment int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_edge WHERE pin_comment IS NOT NULL AND pin_comment != ''`).
		Scan(&withComment); err != nil {
		t.Fatalf("counting pin comments: %v", err)
	}
	if withComment != 2 {
		t.Errorf("edges carrying a pin comment = %d, want 2", withComment)
	}

	run.AnalysisMode = store.AnalysisModeExtended
	extended, err := s.RecordQueryRun(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := ing.EdgesFromBQRS(f, ingest.Options{
		Repository: "octo-org/app", QueryRunID: extended,
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"abom_edge", "abom_edge_regular", "abom_edge_extended"} {
		var count int
		if err := s.DB().QueryRow("SELECT COUNT(*) FROM " + source).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != total {
			t.Errorf("replayed CodeQL evidence: %s has %d edges, want %d", source, count, total)
		}
	}
}

// bqrsPathJSON decodes the BQRS to JSON, which is the form the ingester consumes.
func bqrsPathJSON(t *testing.T, bqrsPath string) string {
	t.Helper()
	jsonPath := bqrsPath + ".json"

	out, err := os.Create(jsonPath)
	if err != nil {
		t.Fatalf("creating %s: %v", jsonPath, err)
	}
	defer out.Close()

	cmd := exec.Command("codeql", "bqrs", "decode", "--format=json", "--entities=all", bqrsPath)
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		t.Fatalf("decoding bqrs: %v", err)
	}
	return jsonPath
}
