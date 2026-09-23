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

// Progress derives the steps a query version on disk has taken, from the files in root and the
// test results on record for its id.
func Progress(root string, q Query, tests []store.QueryTest) ([]Step, error) {
	digest, err := DigestTests(root, q.Tests)
	if err != nil {
		return nil, err
	}
	var current, compiledElsewhere, passedElsewhere *store.QueryTest
	for i := range tests {
		t := &tests[i]
		switch {
		case t.QueryVersion == q.Version:
			current = t
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
	database, err := databaseStep(root, q, current, digest)
	if err != nil {
		return nil, err
	}
	return []Step{
		help, expected, database,
		{Step: "ast", State: Pending, Detail: "no PrintAST run is recorded over the test database"},
		compilesStep(current, compiledElsewhere),
		testsStep(q, current, passedElsewhere, digest),
	}, nil
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
func databaseStep(root string, q Query, current *store.QueryTest, digest string) (Step, error) {
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
		step, err := testDatabaseStep(root, dir, current, digest)
		if err != nil {
			return Step{}, err
		}
		steps = append(steps, step)
	}
	return worst(steps), nil
}

func testDatabaseStep(root, dir string, current *store.QueryTest, digest string) (Step, error) {
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
	finished, err := extractionFinished(filepath.Join(db, "codeql-database.yml"))
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
	return Step{Step: "database", State: Passing, Detail: relTo(root, db) + ", extracted " +
		info.ModTime().UTC().Format(time.RFC3339)}, nil
}

// extractionFinished reads whether the CLI finalized a database, which it records in its metadata.
func extractionFinished(yml string) (bool, error) {
	f, err := os.Open(yml)
	if err != nil {
		return false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == finishedLine {
			return true, nil
		}
	}
	return false, scanner.Err()
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

func compilesStep(current, elsewhere *store.QueryTest) Step {
	step := Step{Step: "compiles", State: Pending, Detail: "not compiled for this version"}
	switch {
	case current != nil && current.FailureStage == "COMPILATION":
		step.State, step.Detail = Failed, current.Detail
	case current != nil && compiled(*current):
		step.State, step.Detail = Passing, "compiled when its tests ran at "+current.RanAt
	case current != nil && current.FailureStage == "EXTRACTION":
		step.Detail = "the test code did not extract, so compiling was not reported"
	case elsewhere != nil:
		step.State = Warning
		step.Detail = fmt.Sprintf("version %s compiled at %s; this version has not been compiled",
			short(elsewhere.QueryVersion), elsewhere.RanAt)
	}
	return step
}

func testsStep(q Query, current, elsewhere *store.QueryTest, digest string) Step {
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
	case elsewhere != nil:
		step.State = Warning
		step.Detail = fmt.Sprintf("version %s passed at %s; this version has not been tested",
			short(elsewhere.QueryVersion), elsewhere.RanAt)
	}
	return step
}
