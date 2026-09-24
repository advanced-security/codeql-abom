package authoring

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// testCLI is the CodeQL these tests say runs now, unless a test names another.
const testCLI = "2.27.1"

// progressOf is the steps a query has taken under testCLI, with every result that names no
// CodeQL recorded under it.
func progressOf(t *testing.T, root, id string, tests ...store.QueryTest) map[string]Step {
	t.Helper()
	tests = slices.Clone(tests)
	for i := range tests {
		if tests[i].CLIVersion == "" {
			tests[i].CLIVersion = testCLI
		}
	}
	return progressUnder(t, root, id, testCLI, store.AuthoringAST{}, tests...)
}

// progressUnder is the steps a query has taken as the CodeQL named cli would read them.
func progressUnder(t *testing.T, root, id, cli string, printed store.AuthoringAST,
	tests ...store.QueryTest) map[string]Step {
	t.Helper()
	q, found, err := Find(root, id)
	if err != nil || !found {
		t.Fatalf("Find(%s): found=%v err=%v", id, found, err)
	}
	steps, err := Progress(root, q, Evidence{CLI: cli, Tests: tests, Printed: printed})
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	by := map[string]Step{}
	for _, s := range steps {
		by[s.Step] = s
	}
	return by
}

// failedFirstOver is an earlier version's run over these fixtures that missed a marked result.
func failedFirstOver(t *testing.T, root, id string) store.QueryTest {
	t.Helper()
	q, _, err := Find(root, id)
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := DigestFixtures(root, q.Tests)
	if err != nil {
		t.Fatal(err)
	}
	one, none := int64(1), int64(0)
	return store.QueryTest{QueryID: id, QueryVersion: "the-stub", Total: 1, Failed: 1,
		FailureStage: "RESULT", FixturesDigest: fixtures, Missing: &one, Unexpected: &none,
		RanAt: "2026-09-24T09:00:00Z", Detail: "0 of 1 passed; 1 expected result missing, nothing unexpected"}
}

func mustVersion(t *testing.T, root, id string) (Query, string) {
	t.Helper()
	q, _, err := Find(root, id)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := DigestTests(root, q.Tests)
	if err != nil {
		t.Fatal(err)
	}
	return q, digest
}

// testDatabase writes what the CLI leaves in a test directory's database, dated at.
func testDatabase(t *testing.T, root, dir string, finished bool, at time.Time) {
	t.Helper()
	name := filepath.Base(dir)
	body := "---\nprimaryLanguage: actions\n" + finishedLine + "\n"
	if !finished {
		body = strings.Replace(body, "true", "false", 1)
	}
	path := write(t, root, dir+"/"+name+".testproj/codeql-database.yml", body)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func touch(t *testing.T, root, rel string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), at, at); err != nil {
		t.Fatal(err)
	}
}

// The strip is read left to right, so the order is part of what it says.
func TestTheStepsAreReportedInTheirOrder(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, _, err := Find(root, "acme/a")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := Progress(root, q, Evidence{})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(steps))
	for _, s := range steps {
		names = append(names, s.Step)
	}
	if got, want := strings.Join(names, " "), "help expected database ast compiles tests"; got != want {
		t.Errorf("steps %q, want %q", got, want)
	}
}

// A step nobody has taken is not a step that went wrong, and reporting it as failed turns an
// absence into a measurement.
func TestAQueryNothingHasBeenDoneForIsPendingThroughout(t *testing.T) {
	root := workspace(t, nil)
	write(t, root, "src/A.ql", query("acme/a"))

	for name, step := range progressOf(t, root, "acme/a") {
		if step.State != Pending {
			t.Errorf("%s reads %s (%s) with nothing done", name, step.State, step.Detail)
		}
	}
}

func TestHelpBesideTheQueryPassesAndAnEmptyFileDoesNot(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	if got := progressOf(t, root, "acme/a")["help"]; got.State != Pending {
		t.Errorf("no help reads %s", got.State)
	}
	write(t, root, "src/A.md", "   \n")
	if got := progressOf(t, root, "acme/a")["help"]; got.State != Pending {
		t.Errorf("an empty help file reads %s", got.State)
	}
	write(t, root, "src/A.md", "# A\n\nFinds jobs.\n")
	if got := progressOf(t, root, "acme/a")["help"]; got.State != Passing {
		t.Errorf("written help reads %s (%s)", got.State, got.Detail)
	}
}

// An empty .expected claims the NON_COMPLIANT example yields nothing, which contradicts the
// example: evidence, and wrong, rather than absent.
func TestAnEmptyExpectedIsAWarningAndAMissingOneIsPending(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	if got := progressOf(t, root, "acme/a")["expected"]; got.State != Passing {
		t.Errorf("a written .expected reads %s (%s)", got.State, got.Detail)
	}
	write(t, root, "test/A/A.expected", "")
	if got := progressOf(t, root, "acme/a")["expected"]; got.State != Warning {
		t.Errorf("an empty .expected reads %s", got.State)
	}
	if err := os.Remove(filepath.Join(root, "test", "A", "A.expected")); err != nil {
		t.Fatal(err)
	}
	if got := progressOf(t, root, "acme/a")["expected"]; got.State != Pending {
		t.Errorf("a missing .expected reads %s", got.State)
	}
}

// A failed extraction leaves a database behind that says it is unfinished, so the directory being
// there is not evidence the test code extracted.
func TestTheTestDatabaseIsReadFromWhetherItFinished(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name     string
		finished bool
		builtAt  time.Time
		want     State
	}{
		{"finished after its sources", true, now, Passing},
		{"finished before its sources changed", true, now.Add(-time.Hour), Warning},
		{"unfinished", false, now, Failed},
	}
	for _, c := range cases {
		root := workspace(t, map[string]string{"A": "acme/a"})
		touch(t, root, "test/A/.github/workflows/ci.yml", now.Add(-time.Minute))
		testDatabase(t, root, "test/A", c.finished, c.builtAt)
		if got := progressOf(t, root, "acme/a")["database"]; got.State != c.want {
			t.Errorf("%s: reads %s (%s), want %s", c.name, got.State, got.Detail, c.want)
		}
	}
}

// The .expected is compared after extraction, so rewriting it does not make the database stale.
func TestEditingTheExpectedLeavesTheTestDatabaseCurrent(t *testing.T) {
	now := time.Now()
	root := workspace(t, map[string]string{"A": "acme/a"})
	touch(t, root, "test/A/.github/workflows/ci.yml", now.Add(-time.Hour))
	testDatabase(t, root, "test/A", true, now.Add(-time.Minute))
	touch(t, root, "test/A/A.expected", now)

	if got := progressOf(t, root, "acme/a")["database"]; got.State != Passing {
		t.Errorf("reads %s (%s) after only the .expected changed", got.State, got.Detail)
	}
}

func TestACompileFailureForThisVersionFailsCompiles(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, digest := mustVersion(t, root, "acme/a")

	got := progressOf(t, root, "acme/a", store.QueryTest{
		QueryID: "acme/a", QueryVersion: q.Version, Total: 1, Failed: 1, TestDigest: digest,
		FailureStage: "COMPILATION", Detail: "0 of 1 passed; it does not compile: A.ql:3:1: oops",
	})
	if got["compiles"].State != Failed || !strings.Contains(got["compiles"].Detail, "A.ql:3:1") {
		t.Errorf("compiles reads %s (%s)", got["compiles"].State, got["compiles"].Detail)
	}
	if got["tests"].State != Failed {
		t.Errorf("tests read %s with the query not compiling", got["tests"].State)
	}
}

// A pass describes the tests it ran. Once they change it is history, and reads as stale rather
// than as green.
func TestAPassForTestsThatHaveSinceChangedIsAWarning(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, digest := mustVersion(t, root, "acme/a")
	pass := store.QueryTest{
		QueryID: "acme/a", QueryVersion: q.Version, Passed: true, Total: 1, TestDigest: digest,
		Detail: "1 of 1 passed",
	}
	first := failedFirstOver(t, root, "acme/a")
	got := progressOf(t, root, "acme/a", first, pass)
	if got["tests"].State != Passing || got["compiles"].State != Passing {
		t.Fatalf("a current pass reads tests=%s compiles=%s", got["tests"].State,
			got["compiles"].State)
	}

	write(t, root, "test/A/A.expected", "| a job |\n| another job |\n")
	if got := progressOf(t, root, "acme/a", first, pass)["tests"]; got.State != Warning {
		t.Errorf("a pass for tests since rewritten reads %s (%s)", got.State, got.Detail)
	}
}

// A pass shows a query agrees with its tests. Only a failure of an earlier version over the same
// fixtures, on a result they mark, shows the tests could have caught a query that disagreed.
func TestAPassIsEvidenceOnlyOnceTheSameFixturesWereSeenFailingForAMarkedResult(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, digest := mustVersion(t, root, "acme/a")
	pass := store.QueryTest{QueryID: "acme/a", QueryVersion: q.Version, Passed: true, Total: 1,
		TestDigest: digest, Detail: "1 of 1 passed", RanAt: "2026-09-24T10:00:00Z"}
	first := failedFirstOver(t, root, "acme/a")
	zero := int64(0)

	for _, c := range []struct {
		name   string
		before []store.QueryTest
		want   State
		says   string
	}{
		{"never seen failing", nil, Warning, "never seen failing"},
		{"failed to compile first", []store.QueryTest{{QueryID: "acme/a", QueryVersion: "broken",
			Total: 1, Failed: 1, FailureStage: "COMPILATION", FixturesDigest: first.FixturesDigest}},
			Warning, "never seen failing"},
		{"failed first for want of an .expected, marking nothing", []store.QueryTest{{
			QueryID: "acme/a", QueryVersion: "the-stub", Total: 1, Failed: 1, FailureStage: "RESULT",
			FixturesDigest: first.FixturesDigest, Missing: &zero, Unexpected: &zero}},
			Warning, "never seen failing"},
		{"failed first over fixtures since changed", []store.QueryTest{func() store.QueryTest {
			f := first
			f.FixturesDigest = "older-fixtures"
			return f
		}()}, Warning, "changed since"},
		{"failed first on a marked result", []store.QueryTest{first}, Passing, "the-stub failed first"},
	} {
		got := progressOf(t, root, "acme/a", append(c.before, pass)...)["tests"]
		if got.State != c.want || !strings.Contains(got.Detail, c.says) {
			t.Errorf("%s: %+v, want %s saying %q", c.name, got, c.want, c.says)
		}
	}
}

// A result under one CodeQL says nothing about the next: extraction and evaluation both change
// between releases, so every mark read from one reads as stale under another.
func TestEvidenceFromAnotherCodeQLReadsAsStale(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, digest := mustVersion(t, root, "acme/a")
	fixtures, err := DigestFixtures(root, q.Tests)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	touch(t, root, "test/A/.github/workflows/ci.yml", now.Add(-time.Hour))
	write(t, root, "test/A/A.testproj/codeql-database.yml",
		"---\ncreationMetadata:\n  cliVersion: 2.27.0\n"+finishedLine+"\n")
	first := failedFirstOver(t, root, "acme/a")
	first.CLIVersion = "2.27.0"
	pass := store.QueryTest{QueryID: "acme/a", QueryVersion: q.Version, CLIVersion: "2.27.0",
		Passed: true, Total: 1, TestDigest: digest, Detail: "1 of 1 passed"}
	printed := store.AuthoringAST{RowID: 1, TestDigest: fixtures, Size: 10, CLIVersion: "2.27.0",
		PrintedAt: "2026-09-24T10:00:00Z"}

	under := func(cli string) map[string]Step {
		return progressUnder(t, root, "acme/a", cli, printed, first, pass)
	}
	for _, step := range []string{"database", "ast", "compiles", "tests"} {
		if got := under("2.27.0")[step]; got.State != Passing {
			t.Errorf("%s under the CodeQL that ran it reads %+v", step, got)
		}
		if got := under("2.27.1")[step]; got.State != Warning || !strings.Contains(got.Detail, "2.27.0") {
			t.Errorf("%s under a newer CodeQL reads %+v, want a warning naming 2.27.0", step, got)
		}
	}
}

// A server that cannot read which CodeQL it runs cannot say anything on record is current, and a
// result kept before ABOM recorded the CodeQL is not thereby the current one.
func TestAnUnreadableCodeQLLeavesNothingOnRecordCurrent(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, digest := mustVersion(t, root, "acme/a")
	fixtures, err := DigestFixtures(root, q.Tests)
	if err != nil {
		t.Fatal(err)
	}
	for _, recorded := range []string{"", "2.27.1"} {
		first := failedFirstOver(t, root, "acme/a")
		first.CLIVersion = recorded
		pass := store.QueryTest{QueryID: "acme/a", QueryVersion: q.Version, CLIVersion: recorded,
			Passed: true, Total: 1, TestDigest: digest, Detail: "1 of 1 passed"}
		printed := store.AuthoringAST{RowID: 1, TestDigest: fixtures, Size: 10, CLIVersion: recorded,
			PrintedAt: "2026-09-24T10:00:00Z"}

		got := progressUnder(t, root, "acme/a", "", printed, first, pass)
		for _, step := range []string{"ast", "compiles", "tests"} {
			if got[step].State == Passing || !strings.Contains(got[step].Detail, "could not read which CodeQL") {
				t.Errorf("recorded under %q, %s reads %+v with no CodeQL readable", recorded, step, got[step])
			}
		}
	}
}

// A query compiled against one actions library and run by ABOM against another can answer
// differently, and nothing downstream would say which one did. A lock resolving another version
// than the one ABOM's queries run reads as stale, however the tests went.
func TestCompilingAgainstAnotherActionsLibraryIsAWarning(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, digest := mustVersion(t, root, "acme/a")
	pass := store.QueryTest{QueryID: "acme/a", QueryVersion: q.Version, Passed: true, Total: 1,
		TestDigest: digest, Detail: "1 of 1 passed"}
	lock := func(pack, version string) {
		write(t, root, pack+"codeql-pack.lock.yml", "---\nlockVersion: 1.0.0\ndependencies:\n"+
			"  codeql/actions-all:\n    version: "+version+"\n  codeql/util:\n    version: 2.0.45\n")
	}
	if got := progressOf(t, root, "acme/a", pass)["compiles"]; got.State != Passing {
		t.Errorf("with no lock written yet, compiles reads %+v", got)
	}
	lock("", customql.ActionsAll)
	lock("test/", customql.ActionsAll)
	if got := progressOf(t, root, "acme/a", pass)["compiles"]; got.State != Passing {
		t.Errorf("locked to ABOM's library, compiles reads %+v", got)
	}
	lock("test/", "0.6.1")
	got := progressOf(t, root, "acme/a", pass)["compiles"]
	if got.State != Warning || !strings.Contains(got.Detail, "0.6.1") ||
		!strings.Contains(got.Detail, customql.ActionsAll) || !strings.Contains(got.Detail, "test/") {
		t.Errorf("with the test pack locked to 0.6.1, compiles reads %+v", got)
	}
}

// Accepting a report that records failed expectations makes the test pass without the query
// reporting what the fixtures mark, because the CLI compares the output as text.
func TestAnExpectedThatAcceptsFailedExpectationsFails(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	write(t, root, "test/A/A.expected", "#select\ntestFailures\n"+
		"| .github/workflows/ci.yml:6:35:6:43 | # $ Alert | Missing result: Alert |\n")
	if got := progressOf(t, root, "acme/a")["expected"]; got.State != Failed ||
		!strings.Contains(got.Detail, "1 failed expectation") {
		t.Errorf("an .expected accepting a missing result reads %+v", got)
	}
}

// An edited query has passed nothing. That another version passed is worth saying, and is not a
// pass for this one.
func TestOnlyAnotherVersionPassingIsAWarning(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	_, digest := mustVersion(t, root, "acme/a")

	got := progressOf(t, root, "acme/a", store.QueryTest{
		QueryID: "acme/a", QueryVersion: "an-earlier-version", Passed: true, Total: 1,
		TestDigest: digest, Detail: "1 of 1 passed",
	})
	for _, step := range []string{"compiles", "tests"} {
		if got[step].State != Warning {
			t.Errorf("%s reads %s (%s) when only another version was tested", step,
				got[step].State, got[step].Detail)
		}
	}
}

// The AST step is read from the newest tree kept for the query, and passes only for a tree printed
// from the fixtures on disk now: one printed before they changed describes files that are gone.
func TestTheASTStepReadsTheNewestTreeKeptForTheCurrentFixtures(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, _ := mustVersion(t, root, "acme/a")
	digest, err := DigestFixtures(root, q.Tests)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name    string
		printed store.AuthoringAST
		want    State
		says    string
	}{
		{"nothing kept", store.AuthoringAST{}, Pending, "no PrintAST tree"},
		{"kept from these fixtures", store.AuthoringAST{RowID: 3, TestDigest: digest, Size: 120,
			PrintedAt: "2026-09-23T12:00:00Z"}, Passing, "2026-09-23T12:00:00Z"},
		{"kept from changed fixtures", store.AuthoringAST{RowID: 2, TestDigest: "other", Size: 120,
			PrintedAt: "2026-09-22T12:00:00Z"}, Warning, "changed"},
		{"kept with no test to say which fixtures", store.AuthoringAST{RowID: 5, Size: 120,
			PrintedAt: "2026-09-22T12:00:00Z"}, Warning, "changed"},
		{"printed nothing", store.AuthoringAST{RowID: 4, TestDigest: digest,
			PrintedAt: "2026-09-23T12:00:00Z"}, Warning, "nothing"},
	} {
		printed := c.printed
		if printed.RowID != 0 {
			printed.CLIVersion = testCLI
		}
		steps, err := Progress(root, q, Evidence{CLI: testCLI, Printed: printed})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range steps {
			if s.Step == "ast" && (s.State != c.want || !strings.Contains(s.Detail, c.says)) {
				t.Errorf("%s: %+v, want %s saying %q", c.name, s, c.want, c.says)
			}
		}
	}
}

// A session prints its tree before its report is accepted, and accepting rewrites the .expected
// and nothing the tree was printed from; changing a fixture is what makes the tree old.
func TestAcceptingAReportLeavesTheTreeCurrentAndChangingAFixtureDoesNot(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, _ := mustVersion(t, root, "acme/a")
	fixtures, err := DigestFixtures(root, q.Tests)
	if err != nil {
		t.Fatal(err)
	}
	printed := store.AuthoringAST{RowID: 3, TestDigest: fixtures, Size: 120, PrintedAt: "2026-09-23T12:00:00Z",
		CLIVersion: testCLI}
	ast := func() Step {
		t.Helper()
		steps, err := Progress(root, q, Evidence{CLI: testCLI, Printed: printed})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range steps {
			if s.Step == "ast" {
				return s
			}
		}
		t.Fatal("no ast step")
		return Step{}
	}

	write(t, root, "test/A/A.expected", "| a job | as accepted |\n")
	if s := ast(); s.State != Passing {
		t.Errorf("after the report was accepted the tree reads %+v, want it current", s)
	}
	write(t, root, "test/A/.github/workflows/ci.yml", "on: pull_request\n")
	if s := ast(); s.State != Warning {
		t.Errorf("after a fixture changed the tree reads %+v, want it old", s)
	}
}

// (a) says who chose the target, because "selected by you" and a target nobody is recorded as
// choosing are different claims.
func TestTheTargetStepSaysWhoChoseTheTarget(t *testing.T) {
	for _, c := range []struct {
		name    string
		request store.QueryRequest
		want    State
		says    string
	}{
		{"no target", store.QueryRequest{}, Pending, "agent"},
		{"chosen by the operator", store.QueryRequest{QueryID: "acme/a",
			TargetSource: store.TargetOperator}, Passing, "selected by you"},
		{"chosen by the agent", store.QueryRequest{QueryID: "acme/a",
			TargetSource: store.TargetAgent}, Passing, "decided by the agent"},
		{"chosen by the agent, saying why", store.QueryRequest{QueryID: "acme/a",
			TargetSource: store.TargetAgent, TargetDetail: "nothing in the pack covers pins"},
			Passing, "decided by the agent: nothing in the pack covers pins"},
		{"kept before choosers were recorded", store.QueryRequest{QueryID: "acme/a"}, Warning,
			"nobody is recorded"},
	} {
		got := TargetStep(c.request)
		if got.Step != "target" || got.State != c.want || !strings.Contains(got.Detail, c.says) {
			t.Errorf("%s: %+v, want %s saying %q", c.name, got, c.want, c.says)
		}
	}
}

// With nothing on disk to describe, every step is waiting, in the same order as a query's.
func TestAQueryNotInTheWorkspaceHasEveryStepWaiting(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, _, err := Find(root, "acme/a")
	if err != nil {
		t.Fatal(err)
	}
	real, err := Progress(root, q, Evidence{})
	if err != nil {
		t.Fatal(err)
	}
	waiting := Unstarted("nothing in the query directory implements it yet")
	if len(waiting) != len(real) {
		t.Fatalf("%d waiting steps for %d real ones", len(waiting), len(real))
	}
	for i := range waiting {
		if waiting[i].Step != real[i].Step || waiting[i].State != Pending {
			t.Errorf("step %d is %+v, want %s pending", i, waiting[i], real[i].Step)
		}
	}
}

// extended-test is earned the way test is: refused while any step is not passing, naming the
// first one in the strip's order so the reader knows what to do next.
func TestTheRungAfterTestWaitsForTheFirstStepThatIsNotPassing(t *testing.T) {
	passing := func(names ...string) []Step {
		out := []Step{}
		for _, name := range names {
			out = append(out, Step{Step: name, State: Passing})
		}
		return out
	}
	all := passing("help", "expected", "database", "ast", "compiles", "tests")
	if refusal := ExtendedTestRefusal(all); refusal != "" {
		t.Errorf("every step passing was refused: %s", refusal)
	}

	stale := append([]Step{}, all...)
	stale[3] = Step{Step: "ast", State: Pending, Detail: "no PrintAST run is recorded"}
	stale[5] = Step{Step: "tests", State: Warning, Detail: "passed before the tests changed"}
	refusal := ExtendedTestRefusal(stale)
	if !strings.Contains(refusal, "AST") || !strings.Contains(refusal, "no PrintAST run") ||
		strings.Contains(refusal, "Tests") {
		t.Errorf("refused with %q, want the AST step named and nothing after it", refusal)
	}
	if ExtendedTestRefusal(nil) == "" {
		t.Error("a query with no steps at all was let through")
	}
}
