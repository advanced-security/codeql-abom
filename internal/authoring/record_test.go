package authoring

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func recordingStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// A recorded run is evidence only with what it ran under: the CodeQL, the tests, the fixtures and
// what its expectations said.
func TestARecordedRunNamesItsCodeQLFixturesAndExpectations(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, digest := mustVersion(t, root, "acme/a")
	fixtures, err := DigestFixtures(root, q.Tests)
	if err != nil {
		t.Fatal(err)
	}
	st := recordingStore(t)
	one, none := int64(1), int64(0)
	run := func(context.Context, []string) (runner.TestResult, error) {
		return runner.TestResult{Total: 1, Failed: 1, FailureStage: "RESULT", Missing: &one,
			Unexpected: &none, Detail: "0 of 1 passed; 1 expected result missing, nothing unexpected"}, nil
	}

	if _, err := RecordTests(context.Background(), st, run, root, q, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	got, known, err := st.QueryTestFor("acme/a", q.Version, "2.27.1")
	if err != nil || !known {
		t.Fatalf("nothing recorded: known=%v err=%v", known, err)
	}
	if got.TestDigest != digest || got.FixturesDigest != fixtures || got.Missing == nil ||
		*got.Missing != 1 || got.Unexpected == nil || *got.Unexpected != 0 {
		t.Errorf("recorded %+v", got)
	}
}

// A result for a query that changed under it describes neither version, and one the CLI could not
// produce is no answer about the query; neither is recorded.
func TestARunThatIsNoAnswerAboutTheVersionIsNotRecorded(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, _ := mustVersion(t, root, "acme/a")
	st := recordingStore(t)
	for name, c := range map[string]struct {
		run  Tests
		want error
	}{
		"the query changed while it ran": {func(context.Context, []string) (runner.TestResult, error) {
			write(t, root, "src/A.ql", query("acme/a")+"// edited\n")
			return runner.TestResult{Total: 1, Passed: true}, nil
		}, ErrChangedDuringTests},
		"the CLI could not run it": {func(context.Context, []string) (runner.TestResult, error) {
			return runner.TestResult{}, errors.New("codeql: not found")
		}, ErrTestsDidNotRun},
	} {
		if _, err := RecordTests(context.Background(), st, c.run, root, q, "2.27.1"); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if tests, err := st.QueryTestsOf("acme/a"); err != nil || len(tests) != 0 {
			t.Errorf("%s: recorded %+v (%v)", name, tests, err)
		}
		write(t, root, "src/A.ql", query("acme/a"))
	}
}
