package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testRunFixture is what CodeQL 2.27.1 printed for `codeql test run <dir> --format=json
// --keep-databases` over a scaffolded workspace: as written, with a line added to the .expected,
// with the query made invalid, and with the workflow made unparsable.
func testRunFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "testrun-"+name+".json"))
	if err != nil {
		t.Fatalf("reading the %s fixture: %v", name, err)
	}
	return body
}

// Where a test stopped is what tells a broken query from a broken fixture and from a query that
// answers differently, and the authoring steps are read from it.
func TestAFailingTestSaysWhichStageItStoppedAt(t *testing.T) {
	for fixture, want := range map[string]string{
		"pass": "", "result": "RESULT", "compilation": "COMPILATION", "extraction": "EXTRACTION",
	} {
		got, err := parseTestOutput(testRunFixture(t, fixture))
		if err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}
		if got.FailureStage != want {
			t.Errorf("%s: stopped at %q, want %q", fixture, got.FailureStage, want)
		}
		if got.Passed != (fixture == "pass") || got.Total != 1 {
			t.Errorf("%s: passed=%v total=%d, want one test that %s", fixture, got.Passed, got.Total,
				map[bool]string{true: "passed", false: "failed"}[fixture == "pass"])
		}
	}
}

// A compile error is only something to act on with the place it was found.
func TestACompileFailureSaysWhereTheQueryIsWrong(t *testing.T) {
	got, err := parseTestOutput(testRunFixture(t, "compilation"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SelfHostedRunner.ql:18:1", "unexpected input 'select'"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail %q does not say %q", got.Detail, want)
		}
	}
}

// An extraction failure carries no position, so what the CLI said is the only account of it.
func TestAnExtractionFailureKeepsWhatTheCLISaid(t *testing.T) {
	got, err := parseTestOutput(testRunFixture(t, "extraction"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Detail, "'codeql test extract' operation failed") {
		t.Errorf("detail %q drops the CLI's description", got.Detail)
	}
}

// A pack whose dependencies were never installed fails to compile with no message at all, only a
// description; "it does not compile" alone sent a reader looking for a mistake in the query.
func TestACompileFailureWithNoMessageKeepsWhatTheCLISaid(t *testing.T) {
	got, err := parseTestOutput(testRunFixture(t, "unresolved"))
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureStage != "COMPILATION" || !strings.Contains(got.Detail, "Could not resolve library path") {
		t.Errorf("stopped at %q saying %q, want the CLI's description", got.FailureStage, got.Detail)
	}
}

func TestAResultFailureSaysTheOutputDiffersFromTheExpected(t *testing.T) {
	got, err := parseTestOutput(testRunFixture(t, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Detail, ".expected") {
		t.Errorf("detail %q does not say the results differ from the .expected", got.Detail)
	}
}

// Some CLI versions print one object per test rather than an array, and the verdict must not
// depend on the framing.
func TestTheVerdictDoesNotDependOnHowTheOutputIsFramed(t *testing.T) {
	var objects []json.RawMessage
	if err := json.Unmarshal(testRunFixture(t, "compilation"), &objects); err != nil {
		t.Fatal(err)
	}
	stream := make([][]byte, 0, len(objects))
	for _, object := range objects {
		stream = append(stream, object)
	}
	got, err := parseTestOutput(bytes.Join(stream, []byte("\n")))
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureStage != "COMPILATION" || got.Total != 1 || got.Passed {
		t.Errorf("a stream read as stage=%q total=%d passed=%v", got.FailureStage, got.Total,
			got.Passed)
	}
}

// The test database is what the AST step reads, and the CLI deletes it after a pass unless told
// to keep it.
func TestATestRunKeepsItsDatabases(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "testrun-pass.json"))
	if err != nil {
		t.Fatal(err)
	}
	stub, argsFile := stubCodeQL(t, "cat "+fixture+"\n")
	r := &Runner{CodeQLPath: stub}

	if _, err := r.RunTests(context.Background(), []string{"/w/test/A/A.qlref"}); err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--keep-databases") {
		t.Errorf("args %q let the CLI delete the test database", args)
	}
}

// One query's tests are the only evidence about it: running the whole tree grades a query by its
// neighbors' tests.
func TestATestRunNamesOnlyTheTestsItWasGiven(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "testrun-pass.json"))
	if err != nil {
		t.Fatal(err)
	}
	// One argument per line: space-joined, two directories and one path holding a space look alike.
	stub, _ := stubCodeQL(t, `printf '%s\n' "$@" > "$(dirname "$0")/argv.txt"`+"\ncat "+fixture+"\n")
	r := &Runner{CodeQLPath: stub}

	if _, err := r.RunTests(context.Background(),
		[]string{"/w/test/A/A.qlref", "/w/test/B/B.qlref"}); err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	args, err := os.ReadFile(filepath.Join(filepath.Dir(stub), "argv.txt"))
	if err != nil {
		t.Fatal(err)
	}
	positional := []string{}
	for _, arg := range strings.Split(strings.TrimSpace(string(args)), "\n") {
		if strings.HasPrefix(arg, "-") {
			break
		}
		positional = append(positional, arg)
	}
	want := []string{"test", "run", "/w/test/A/A.qlref", "/w/test/B/B.qlref"}
	if strings.Join(positional, "|") != strings.Join(want, "|") {
		t.Errorf("args %q run %q, want %q", args, positional, want)
	}
}

// Nothing to run is refused rather than handed to the CLI, which would read no directory as the
// current one.
func TestATestRunWithNothingToRunIsRefused(t *testing.T) {
	stub, argsFile := stubCodeQL(t, "exit 0\n")
	r := &Runner{CodeQLPath: stub}

	if _, err := r.RunTests(context.Background(), nil); err == nil {
		t.Error("a test run naming no directory was accepted")
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("the CLI was invoked with nothing to run")
	}
}

// capturedAt is where the expectations fixtures were captured; the stub CLI prints them with the
// output file moved to where the test put it.
const capturedAt = "/private/tmp/abom-failfirst/test/Stub"

// An authoring session holds the machine-wide lock for its whole life, so a test run on its behalf
// must not queue behind the session that asked for it; any other caller takes the lock.
func TestATestRunForTheHolderOfTheLockDoesNotWaitForIt(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("testdata", "testrun-pass.json"))
	if err != nil {
		t.Fatal(err)
	}
	stub, _ := stubCodeQL(t, "cat "+fixture+"\n")
	r := &Runner{CodeQLPath: stub, Lock: &Lock{}}
	release, err := r.Lock.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, err := r.RunTestsHeld(context.Background(), []string{"/w/test/A/A.qlref"}); err != nil {
		t.Fatalf("RunTestsHeld under the held lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := r.RunTests(ctx, []string{"/w/test/A/A.qlref"}); err == nil {
		t.Error("RunTests ran while another caller held the lock")
	}
}

// runCaptured runs a stub CLI that prints one captured run, with its .actual beside a test.
func runCaptured(t *testing.T, name string) TestResult {
	t.Helper()
	dir := t.TempDir()
	actual, err := os.ReadFile(filepath.Join("testdata", "testrun-"+name+".actual"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Stub.actual"), actual, 0o644); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "testrun-"+name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	stub, _ := stubCodeQL(t, "sed 's#"+capturedAt+"#"+dir+"#g' "+fixture+"\n")
	got, err := (&Runner{CodeQLPath: stub}).RunTests(context.Background(),
		[]string{filepath.Join(dir, "Stub.qlref")})
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	return got
}

// A test marked with the lines its query must report fails before the query exists because those
// results are missing, and that reason is the evidence the test was written first. A failure for
// want of an .expected alone says nothing about the fixtures.
func TestAFailingTestCountsTheExpectationsItsOutputMissed(t *testing.T) {
	for name, want := range map[string][2]int64{"missing": {1, 0}, "unexpected": {0, 1}} {
		got := runCaptured(t, name)
		if got.Missing == nil || got.Unexpected == nil {
			t.Fatalf("%s: the expectations were not counted: %+v", name, got)
		}
		if *got.Missing != want[0] || *got.Unexpected != want[1] {
			t.Errorf("%s: counted %d missing and %d unexpected, want %d and %d", name,
				*got.Missing, *got.Unexpected, want[0], want[1])
		}
		if !strings.Contains(got.Detail, map[string]string{
			"missing": "1 expected result missing", "unexpected": "1 unexpected result"}[name]) {
			t.Errorf("%s: detail %q does not say which expectations failed", name, got.Detail)
		}
	}
}

// A run that stopped before comparing results measured nothing about the expectations, and zero
// would read as a comparison that found every one met.
func TestARunThatNeverComparedResultsCountsNoExpectations(t *testing.T) {
	for _, fixture := range []string{"compilation", "extraction", "pass"} {
		path, err := filepath.Abs(filepath.Join("testdata", "testrun-"+fixture+".json"))
		if err != nil {
			t.Fatal(err)
		}
		stub, _ := stubCodeQL(t, "cat "+path+"\n")
		got, err := (&Runner{CodeQLPath: stub}).RunTests(context.Background(),
			[]string{"/w/test/A/A.qlref"})
		if err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}
		if got.Missing != nil || got.Unexpected != nil {
			t.Errorf("%s: counted missing=%v unexpected=%v, want no count", fixture, got.Missing,
				got.Unexpected)
		}
	}
}
