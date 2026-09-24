package authoring

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// State is how far a step has got, and only evidence moves it.
type State string

const (
	Pending State = "pending"
	Failed  State = "failed"
	Warning State = "warning"
	Passing State = "passing"
)

// severity orders states for a step with several tests, so the one needing attention is shown.
var severity = map[State]int{Passing: 0, Pending: 1, Warning: 2, Failed: 3}

// finishedLine is how `codeql-database.yml` says a database was finalized.
const finishedLine = "finalised: true" //nolint:misspell // the CLI's key, spelled as it writes it

// Step is one mark on a query's way through dev and test.
type Step struct {
	Step   string `json:"step"`
	State  State  `json:"state"`
	Detail string `json:"detail"`
}

// Evidence is what ABOM holds about a query besides its files: the test results on record for its
// id, the newest tree kept for it, and the CodeQL that runs now, under which alone a mark is
// current. Extraction and evaluation both change between releases.
type Evidence struct {
	CLI     string
	Tests   []store.QueryTest
	Printed store.AuthoringAST
}

// Progress derives the steps a query version on disk has taken, from the files in root and the
// evidence on record for it.
func Progress(root string, q Query, ev Evidence) ([]Step, error) {
	digest, err := DigestTests(root, q.Tests)
	if err != nil {
		return nil, err
	}
	fixtures, err := DigestFixtures(root, q.Tests)
	if err != nil {
		return nil, err
	}
	var current, otherCLI, compiledElsewhere, passedElsewhere *store.QueryTest
	for i := range ev.Tests {
		t := &ev.Tests[i]
		switch {
		// An unreadable CodeQL is not one of no version: a result kept before ABOM recorded the
		// CodeQL names none, and would otherwise read as current.
		case t.QueryVersion == q.Version && ev.CLI != "" && t.CLIVersion == ev.CLI:
			current = t
		case t.QueryVersion == q.Version:
			if otherCLI == nil || t.RanAt > otherCLI.RanAt {
				otherCLI = t
			}
		case t.Passed && (passedElsewhere == nil || t.RanAt > passedElsewhere.RanAt):
			passedElsewhere = t
		}
		if t.QueryVersion != q.Version && compiled(*t) &&
			(compiledElsewhere == nil || t.RanAt > compiledElsewhere.RanAt) {
			compiledElsewhere = t
		}
	}

	help, err := helpStep(root, q)
	if err != nil {
		return nil, err
	}
	expected, err := expectedStep(root, q)
	if err != nil {
		return nil, err
	}
	database, err := databaseStep(root, q, current, digest, ev.CLI)
	if err != nil {
		return nil, err
	}
	first, staleFirst := seenFailing(q, ev.Tests, fixtures)
	tests := testsStep(q, current, otherCLI, passedElsewhere, digest, ev.CLI)
	if tests.State == Passing {
		tests = failedFirstStep(q, tests, first, staleFirst)
	}
	compiles := compilesStep(current, otherCLI, compiledElsewhere, ev.CLI)
	if compiles.State == Passing {
		if compiles, err = libraryStep(root, compiles); err != nil {
			return nil, err
		}
	}
	return []Step{
		help, expected, database, astStep(ev.Printed, fixtures, ev.CLI), compiles, tests,
	}, nil
}

// libraryStep is (f) for a version that compiled: a warning where either of the operator's packs
// locked another codeql/actions-all than the one ABOM's queries and ql-mcp's run, since a query
// compiled against one and run against the other can answer differently.
func libraryStep(root string, compiled Step) (Step, error) {
	for _, pack := range []string{"", "test/"} {
		locked, err := lockedActionsAll(filepath.Join(root, filepath.FromSlash(pack), "codeql-pack.lock.yml"))
		if err != nil {
			return Step{}, err
		}
		if locked != "" && locked != customql.ActionsAll {
			compiled.State = Warning
			compiled.Detail = fmt.Sprintf("%s, against codeql/actions-all %s, which %scodeql-pack.lock.yml "+
				"locked; ABOM's queries run %s, so pin it in the manifest and run codeql pack upgrade",
				compiled.Detail, locked, pack, customql.ActionsAll)
			return compiled, nil
		}
	}
	return compiled, nil
}

// lockedActionsAll is the codeql/actions-all a pack's lock file resolved, empty where none has
// been written yet.
func lockedActionsAll(lock string) (string, error) {
	body, err := os.ReadFile(lock)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", err
	}
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "codeql/actions-all:" || i+1 == len(lines) {
			continue
		}
		if version, ok := strings.CutPrefix(strings.TrimSpace(lines[i+1]), "version:"); ok {
			return strings.TrimSpace(version), nil
		}
	}
	return "", nil
}

// FailedOnMarks reports whether a recorded run failed on a result its fixtures mark: at comparing
// results, with an inline expectation unmet. That is a test failing for the reason expected; a
// failure to compile, or for want of an .expected, marks nothing.
func FailedOnMarks(t store.QueryTest) bool {
	return t.FailureStage == "RESULT" && t.Missing != nil && t.Unexpected != nil &&
		*t.Missing+*t.Unexpected > 0
}

// seenFailing is the newest run of another version that failed on a result its fixtures mark: over
// these fixtures, and over fixtures that have since changed.
//
// A pass shows a query agrees with its tests, and only a failure like this shows the tests could
// have caught one that did not.
func seenFailing(q Query, tests []store.QueryTest, fixtures string) (current, stale *store.QueryTest) {
	for i := range tests {
		t := &tests[i]
		if t.QueryVersion == q.Version || !FailedOnMarks(*t) {
			continue
		}
		switch {
		case fixtures != "" && t.FixturesDigest == fixtures:
			if current == nil || t.RanAt > current.RanAt {
				current = t
			}
		case stale == nil || t.RanAt > stale.RanAt:
			stale = t
		}
	}
	return current, stale
}

// failedFirstStep is (g) for a version whose tests pass: passing only once the same fixtures were
// seen failing an earlier version on a result they mark.
func failedFirstStep(q Query, passed Step, first, stale *store.QueryTest) Step {
	switch {
	case first != nil:
		passed.Detail += fmt.Sprintf("; version %s failed first at %s: %s", short(first.QueryVersion),
			first.RanAt, runner.ExpectationsDetail(*first.Missing, *first.Unexpected))
		return passed
	case stale != nil:
		passed.State = Warning
		passed.Detail += fmt.Sprintf(", but the fixtures changed since version %s was seen failing them, "+
			"so nothing shows these tests can fail", short(stale.QueryVersion))
		return passed
	}
	passed.State = Warning
	passed.Detail += ", but these fixtures were never seen failing on a result they mark, so nothing " +
		"shows the tests could catch a query that misses one"
	if !markedInline(q.Tests) {
		passed.Detail += "; mark each line the query must report with # $ Alert, and name " +
			customql.Postprocess + " as the .qlref's postprocess"
	}
	return passed
}

// markedInline reports whether any test names a postprocess query, which is what reads a fixture's
// # $ Alert markers; without one a test can fail only for want of its .expected.
func markedInline(tests []string) bool {
	for _, test := range tests {
		body, err := os.ReadFile(test)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "postprocess:") {
				return true
			}
		}
	}
	return false
}

// astStep is (e): a tree PrintAST printed from the fixtures on disk now, by the CodeQL running now.
func astStep(printed store.AuthoringAST, digest, cli string) Step {
	step := Step{Step: "ast", State: Passing}
	switch {
	case printed.RowID == 0:
		step.State, step.Detail = Pending, "no PrintAST tree is kept for this query's test database"
	case printed.Size == 0:
		step.State, step.Detail = Warning, "PrintAST printed nothing at "+printed.PrintedAt+
			", so the fixtures read as no Actions code"
	case printed.TestDigest == "" || printed.TestDigest != digest:
		step.State, step.Detail = Warning, "printed at "+printed.PrintedAt+
			" from fixtures that have changed since"
	case printed.CLIVersion == "" && cli != "":
		step.State, step.Detail = Warning, "printed at "+printed.PrintedAt+
			" before ABOM recorded which CodeQL printed it; print it again"
	case printed.CLIVersion != cli || cli == "":
		step.State, step.Detail = Warning, fmt.Sprintf("printed by %s at %s; %s",
			codeql(printed.CLIVersion), printed.PrintedAt, againUnder("print it", cli))
	default:
		step.Detail = fmt.Sprintf("printed at %s from these fixtures, %d bytes", printed.PrintedAt, printed.Size)
	}
	return step
}

// TargetStep is (a): which query a request is about, and who chose it.
func TargetStep(r store.QueryRequest) Step {
	step := Step{Step: "target", State: Passing}
	switch {
	case r.QueryID == "":
		step.State, step.Detail = Pending, "no query chosen yet; the agent decides"
	case r.TargetSource == store.TargetOperator:
		step.Detail = r.QueryID + ", selected by you"
	case r.TargetSource == store.TargetAgent:
		step.Detail = r.QueryID + ", decided by the agent"
		if r.TargetDetail != "" {
			step.Detail += ": " + r.TargetDetail
		}
	default:
		step.State = Warning
		step.Detail = "nobody is recorded as choosing " + r.QueryID + "; choose it again to confirm"
	}
	return step
}

// Unstarted is every step of a query with nothing on disk to describe, each saying why.
func Unstarted(detail string) []Step {
	out := make([]Step, 0, len(queryStepNames))
	for _, name := range queryStepNames {
		out = append(out, Step{Step: name, State: Pending, Detail: detail})
	}
	return out
}

// queryStepNames are (b) through (g), in the order Progress reports them.
var queryStepNames = []string{"help", "expected", "database", "ast", "compiles", "tests"}

var stepLabels = map[string]string{
	"target": "Target", "help": "Query help", "expected": "Expected results",
	"database": "Test database", "ast": "AST", "compiles": "Compiles", "tests": "Tests",
}

// ExtendedTestRefusal says why a version with these steps may not enter extended-test, naming the
// first step that is not passing, or is empty when every one is.
func ExtendedTestRefusal(steps []Step) string {
	if len(steps) == 0 {
		return "none of its steps can be shown, so none has passed"
	}
	for _, s := range steps {
		if s.State != Passing {
			return fmt.Sprintf("%s is %s: %s", stepLabels[s.Step], s.State, s.Detail)
		}
	}
	return ""
}

// compiled reports whether a recorded run got past compiling the query.
func compiled(t store.QueryTest) bool {
	return t.Total > 0 && (t.Passed || t.FailureStage == "RESULT")
}

func short(version string) string {
	if len(version) > 12 {
		return version[:12]
	}
	return version
}

func relTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

func worst(steps []Step) Step {
	out := steps[0]
	for _, s := range steps[1:] {
		if severity[s.State] > severity[out.State] {
			out = s
		}
	}
	return out
}

func helpStep(root string, q Query) (Step, error) {
	base := strings.TrimSuffix(q.Path, ".ql")
	for _, ext := range []string{".md", ".qhelp"} {
		body, err := os.ReadFile(base + ext)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return Step{}, err
		case strings.TrimSpace(string(body)) == "":
			return Step{Step: "help", State: Pending, Detail: relTo(root, base+ext) + " is empty"}, nil
		}
		return Step{Step: "help", State: Passing, Detail: relTo(root, base+ext)}, nil
	}
	return Step{Step: "help", State: Pending,
		Detail: "no " + relTo(root, base+".md") + " beside the query"}, nil
}

func expectedStep(root string, q Query) (Step, error) {
	if len(q.Tests) == 0 {
		return Step{Step: "expected", State: Pending, Detail: "no test names " + q.Rel}, nil
	}
	steps := make([]Step, 0, len(q.Tests))
	for _, test := range q.Tests {
		path := strings.TrimSuffix(test, ".qlref") + ".expected"
		rel := relTo(root, path)
		body, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			steps = append(steps, Step{Step: "expected", State: Pending,
				Detail: rel + " has not been written"})
			continue
		case err != nil:
			return Step{}, err
		}
		if missing, unexpected := runner.ExpectationFailures(body); missing+unexpected > 0 {
			n := missing + unexpected
			steps = append(steps, Step{Step: "expected", State: Failed, Detail: fmt.Sprintf(
				"%s accepts %d failed %s, so the test passes without the query reporting what its "+
					"fixtures mark", rel, n, plural(int(n), "expectation", "expectations"))})
			continue
		}
		lines := 0
		for _, line := range strings.Split(string(body), "\n") {
			if strings.TrimSpace(line) != "" {
				lines++
			}
		}
		if lines == 0 {
			steps = append(steps, Step{Step: "expected", State: Warning,
				Detail: rel + " is empty, so the NON_COMPLIANT example is expected to match nothing"})
			continue
		}
		steps = append(steps, Step{Step: "expected", State: Passing,
			Detail: fmt.Sprintf("%s: %d %s", rel, lines, plural(lines, "line", "lines"))})
	}
	return worst(steps), nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// databaseStep reads the database the CLI builds beside each test, which a failed extraction
// leaves behind unfinished.
func databaseStep(root string, q Query, current *store.QueryTest, digest, cli string) (Step, error) {
	if len(q.Tests) == 0 {
		return Step{Step: "database", State: Pending, Detail: "no test names " + q.Rel}, nil
	}
	seen := map[string]bool{}
	steps := []Step{}
	for _, test := range q.Tests {
		dir := filepath.Dir(test)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		step, err := testDatabaseStep(root, dir, current, digest, cli)
		if err != nil {
			return Step{}, err
		}
		steps = append(steps, step)
	}
	return worst(steps), nil
}

func testDatabaseStep(root, dir string, current *store.QueryTest, digest, cli string) (Step, error) {
	db := filepath.Join(dir, filepath.Base(dir)+".testproj")
	info, err := os.Stat(filepath.Join(db, "codeql-database.yml"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if current != nil && current.TestDigest == digest && current.FailureStage == "EXTRACTION" {
			return Step{Step: "database", State: Failed, Detail: current.Detail}, nil
		}
		return Step{Step: "database", State: Pending,
			Detail: relTo(root, dir) + " has not been extracted"}, nil
	case err != nil:
		return Step{}, err
	}
	finished, built, err := databaseMetadata(filepath.Join(db, "codeql-database.yml"))
	if err != nil {
		return Step{}, err
	}
	if !finished {
		return Step{Step: "database", State: Failed,
			Detail: relTo(root, db) + " did not finish extracting"}, nil
	}
	newest, err := newestSource(dir)
	if err != nil {
		return Step{}, err
	}
	if newest.After(info.ModTime()) {
		return Step{Step: "database", State: Warning,
			Detail: relTo(root, db) + " is older than the test code"}, nil
	}
	// A database that names no CodeQL cannot be said to be another's.
	if built != "" && built != cli {
		return Step{Step: "database", State: Warning, Detail: fmt.Sprintf(
			"%s was extracted by CodeQL %s; %s", relTo(root, db), built, againUnder("run the tests", cli))}, nil
	}
	return Step{Step: "database", State: Passing, Detail: relTo(root, db) + ", extracted " +
		info.ModTime().UTC().Format(time.RFC3339)}, nil
}

// databaseMetadata reads whether the CLI finalized a database and which CodeQL built it, both of
// which it records in the database's metadata.
func databaseMetadata(yml string) (finished bool, cli string, err error) {
	f, err := os.Open(yml)
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == finishedLine {
			finished = true
		}
		if version, ok := strings.CutPrefix(line, "cliVersion:"); ok {
			cli = strings.TrimSpace(version)
		}
	}
	return finished, cli, scanner.Err()
}

// extractionFinished reads whether the CLI finalized a database.
func extractionFinished(yml string) (bool, error) {
	finished, _, err := databaseMetadata(yml)
	return finished, err
}

// newestSource is when the test code last changed. The .expected and the .qlref are read after
// extraction, so neither makes a database stale.
func newestSource(dir string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && strings.HasSuffix(d.Name(), ".testproj"):
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		switch filepath.Ext(p) {
		case ".expected", ".qlref", ".ql", ".actual":
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

func compilesStep(current, otherCLI, elsewhere *store.QueryTest, cli string) Step {
	step := Step{Step: "compiles", State: Pending, Detail: "not compiled for this version"}
	switch {
	case current != nil && current.FailureStage == "COMPILATION":
		step.State, step.Detail = Failed, current.Detail
	case current != nil && compiled(*current):
		step.State, step.Detail = Passing, "compiled when its tests ran at "+current.RanAt
	case current != nil && current.FailureStage == "EXTRACTION":
		step.Detail = "the test code did not extract, so compiling was not reported"
	case otherCLI != nil && compiled(*otherCLI):
		step.State = Warning
		step.Detail = fmt.Sprintf("compiled under %s when its tests ran at %s; %s",
			codeql(otherCLI.CLIVersion), otherCLI.RanAt, againUnder("run them", cli))
	case elsewhere != nil:
		step.State = Warning
		step.Detail = fmt.Sprintf("version %s compiled at %s; this version has not been compiled",
			short(elsewhere.QueryVersion), elsewhere.RanAt)
	}
	return step
}

// codeql names the CodeQL a result was recorded under, which is unknown for one kept before it was.
func codeql(version string) string {
	if version == "" {
		return "a CodeQL ABOM did not record"
	}
	return "CodeQL " + version
}

// againUnder asks for something to be done again under the CodeQL this server runs, or says it
// cannot, since a server that could not read its CodeQL cannot name one to do it under.
func againUnder(what, cli string) string {
	if cli == "" {
		return "this server could not read which CodeQL it runs, so this is not known to be current"
	}
	return what + " again under " + cli
}

func testsStep(q Query, current, otherCLI, elsewhere *store.QueryTest, digest, cli string) Step {
	step := Step{Step: "tests", State: Pending, Detail: "never run for this version"}
	switch {
	case len(q.Tests) == 0:
		step.Detail = "no test names " + q.Rel
	case current != nil && current.Total > 0 && current.TestDigest != digest:
		outcome := map[bool]string{true: "passed", false: "failed"}[current.Passed]
		step.State = Warning
		step.Detail = outcome + " before the tests changed; run them again"
		if current.TestDigest == "" {
			step.Detail = outcome + " before ABOM recorded which tests ran; run them again"
		}
	case current != nil && current.Total > 0 && current.Passed:
		step.State, step.Detail = Passing, current.Detail+" at "+current.RanAt
	case current != nil && current.Total > 0:
		step.State, step.Detail = Failed, current.Detail
	case otherCLI != nil && otherCLI.Total > 0:
		outcome := map[bool]string{true: "passed", false: "failed"}[otherCLI.Passed]
		step.State = Warning
		step.Detail = fmt.Sprintf("%s under %s at %s; %s", outcome,
			codeql(otherCLI.CLIVersion), otherCLI.RanAt, againUnder("run them", cli))
	case elsewhere != nil:
		step.State = Warning
		step.Detail = fmt.Sprintf("version %s passed at %s; this version has not been tested",
			short(elsewhere.QueryVersion), elsewhere.RanAt)
	}
	return step
}
