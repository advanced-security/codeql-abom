package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeResults makes a stub CLI that produces the BQRS `database run-queries` would, under the
// nested pack directory it actually writes to.
func stubRunQueries(t *testing.T, pack string, queries ...string) (path, argsFile string) {
	t.Helper()
	var script strings.Builder
	for _, q := range queries {
		// $1.. holds the arguments; the database is the one after the -- separator, which is
		// where the results directory lives.
		script.WriteString(`mkdir -p "$DB/results/` + pack + `"` + "\n")
		script.WriteString(`printf bqrs > "$DB/results/` + pack + `/` + q + `.bqrs"` + "\n")
	}
	return stubCodeQL(t, `DB=$(echo "$@" | sed 's/.*-- //' | cut -d' ' -f1)`+"\n"+script.String()+"exit 0\n")
}

// One invocation rather than one per query is the whole point: three `query run` processes cost
// 11.76 s a repository against 4.34 s for a single `database run-queries`, because the waste is
// per process and not per query.
func TestEveryQueryIsEvaluatedInOneInvocation(t *testing.T) {
	db := t.TempDir()
	stub, argsFile := stubRunQueries(t, "acme/pack", "AbomEdges", "AbomSurface")
	r := &Runner{CodeQLPath: stub}

	if _, err := r.RunQueries(context.Background(), db,
		[]string{"/q/AbomEdges.ql", "/q/AbomSurface.ql"}); err != nil {
		t.Fatalf("RunQueries: %v", err)
	}

	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	args := string(recorded)
	if n := strings.Count(args, "\n"); n != 1 {
		t.Errorf("the CLI was invoked %d times, want once for the whole set", n)
	}
	for _, want := range []string{"database", "run-queries", "-- " + db,
		"/q/AbomEdges.ql", "/q/AbomSurface.ql"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

// The caller asked for a query path and has to be given back the results of that query. CodeQL
// names its output after the pack and the query rather than after anything the caller chose, so
// this mapping is the whole reason the batch form needs a wrapper at all.
func TestResultsComeBackKeyedByTheQueryThatWasAskedFor(t *testing.T) {
	db := t.TempDir()
	stub, _ := stubRunQueries(t, "acme/pack", "AbomEdges", "AbomSurface")
	r := &Runner{CodeQLPath: stub}

	got, err := r.RunQueries(context.Background(), db,
		[]string{"/q/AbomEdges.ql", "/q/AbomSurface.ql"})
	if err != nil {
		t.Fatalf("RunQueries: %v", err)
	}

	want := filepath.Join(db, "results", "acme/pack", "AbomEdges.bqrs")
	if got["/q/AbomEdges.ql"] != want {
		t.Errorf("AbomEdges.ql = %q, want %q", got["/q/AbomEdges.ql"], want)
	}
	if len(got) != 2 {
		t.Errorf("got %d results for 2 queries", len(got))
	}
}

// A query that produced nothing is not a query that produced no rows: an empty result set is
// still a BQRS file. Silently returning a short map would have the edges lane ingest whatever
// happened to be there and settle the repository as analyzed.
func TestAQueryThatWroteNoResultsIsAnError(t *testing.T) {
	db := t.TempDir()
	stub, _ := stubRunQueries(t, "acme/pack", "AbomEdges")
	r := &Runner{CodeQLPath: stub}

	_, err := r.RunQueries(context.Background(), db,
		[]string{"/q/AbomEdges.ql", "/q/AbomSurface.ql"})
	if err == nil {
		t.Fatal("a missing result was accepted")
	}
	if !strings.Contains(err.Error(), "AbomSurface.ql") {
		t.Errorf("error does not name the query that produced nothing: %v", err)
	}
}

// Two packs may each hold an `Unpinned.ql`, and the results are told apart only by the pack
// directory above them. Guessing which is which would attribute one pack's results to the other,
// so the ambiguity is refused before anything is evaluated.
func TestTwoQueriesWithTheSameFileNameAreRefused(t *testing.T) {
	db := t.TempDir()
	stub, argsFile := stubRunQueries(t, "acme/pack", "Unpinned")
	r := &Runner{CodeQLPath: stub}

	_, err := r.RunQueries(context.Background(), db,
		[]string{"/a/Unpinned.ql", "/b/Unpinned.ql"})
	if err == nil {
		t.Fatal("two queries with one file name were accepted")
	}
	if _, statErr := os.Stat(argsFile); statErr == nil {
		t.Error("the ambiguity was found only after paying for the evaluation")
	}
}

// The results directory belongs to the database, not to a run, and `--no-rerun` exists precisely
// because CodeQL will leave earlier output in place. Collecting it would report a previous
// query's answer as this one's.
func TestResultsLeftByAnEarlierRunAreNotCollected(t *testing.T) {
	db := t.TempDir()
	stale := filepath.Join(db, "results", "acme/pack", "AbomSurface.bqrs")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	stub, _ := stubRunQueries(t, "acme/pack", "AbomEdges")
	r := &Runner{CodeQLPath: stub}

	_, err := r.RunQueries(context.Background(), db,
		[]string{"/q/AbomEdges.ql", "/q/AbomSurface.ql"})
	if err == nil {
		t.Fatal("a result from an earlier run was accepted as this run's")
	}
}

func TestRunQueriesPropagatesFailure(t *testing.T) {
	stub, _ := stubCodeQL(t, "echo 'compilation failed' >&2\nexit 2\n")
	r := &Runner{CodeQLPath: stub}

	_, err := r.RunQueries(context.Background(), t.TempDir(), []string{"/q/A.ql"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "compilation failed") {
		t.Errorf("error did not include stderr: %v", err)
	}
}

// Nothing outstanding is a legitimate plan once the per-lane skip has taken its share, and
// invoking CodeQL to evaluate no queries would spend a JVM to be told so.
func TestNoQueriesRunsNothing(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	got, err := r.RunQueries(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("RunQueries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results for no queries", len(got))
	}
	if _, statErr := os.Stat(argsFile); statErr == nil {
		t.Error("CodeQL was invoked with nothing to evaluate")
	}
}
