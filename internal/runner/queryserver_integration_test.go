package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// itestDatabaseEnv points at a prebuilt CodeQL actions database. Building one takes far longer
// than a unit test should, so the protocol check runs only when a database is supplied.
// `make itest` builds it and sets this.
const itestDatabaseEnv = "ABOM_ITEST_DB"

func graphQueries(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	src := filepath.Join(root, "ql", "actions", "graph-extraction", "src")
	return []string{
		filepath.Join(src, "AbomEdges.ql"),
		filepath.Join(src, "AbomSurface.ql"),
		filepath.Join(src, "AbomStepConditions.ql"),
	}
}

func realDatabase(t *testing.T) string {
	t.Helper()
	db := os.Getenv(itestDatabaseEnv)
	if db == "" {
		t.Skipf("set %s to a prebuilt actions database to run this test", itestDatabaseEnv)
	}
	if _, err := exec.LookPath("codeql"); err != nil {
		t.Skip("codeql CLI not on PATH")
	}
	return db
}

// The query server's protocol is JSON-RPC with framed messages and interleaved progress, and it
// is the CLI's IDE-facing plumbing rather than a stable interface. Nothing about a mismatch is
// visible until a real server answers, so this is the only test that proves the field names.
func TestTheQueryServerEvaluatesAgainstARealDatabase(t *testing.T) {
	db := realDatabase(t)
	queries := graphQueries(t)

	server := &QueryServer{}
	t.Cleanup(func() { _ = server.Close() })

	out, err := server.Evaluate(context.Background(), db, queries, t.TempDir())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	for _, q := range queries {
		path, ok := out[q]
		if !ok {
			t.Fatalf("no result for %s", q)
		}
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Errorf("%s wrote no usable BQRS at %s (%v)", q, path, err)
		}
	}
}

// A session that has to be restarted for every repository is a session that saves nothing, and a
// second batch is where anything held per-database would show up as a stale or refused answer.
func TestTheQueryServerAnswersASecondBatchOnTheSameSession(t *testing.T) {
	db := realDatabase(t)
	queries := graphQueries(t)

	server := &QueryServer{}
	t.Cleanup(func() { _ = server.Close() })

	if _, err := server.Evaluate(context.Background(), db, queries, t.TempDir()); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if _, err := server.Evaluate(context.Background(), db, queries, t.TempDir()); err != nil {
		t.Fatalf("second batch on the same session: %v", err)
	}
}

// The evaluator is an optimization, so a crawl must survive one that will not talk. Anything
// else makes the fast path load-bearing.
func TestABrokenEvaluatorFallsBackToItsOwnProcess(t *testing.T) {
	db := realDatabase(t)
	queries := graphQueries(t)

	r := &Runner{Evaluator: &QueryServer{CodeQLPath: filepath.Join(t.TempDir(), "not-codeql")}}
	out, err := r.RunQueries(context.Background(), db, queries)
	if err != nil {
		t.Fatalf("RunQueries did not fall back: %v", err)
	}
	if len(out) != len(queries) {
		t.Errorf("got %d results for %d queries", len(out), len(queries))
	}
}
