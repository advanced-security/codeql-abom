package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// TestResult is what `codeql test run` made of one query's tests.
type TestResult struct {
	// Passed is true only when every test passed. A directory holding no tests at all is not a
	// pass: nothing was shown to work, and reporting it as green is the same failure as an
	// unmeasured value printed as zero.
	Passed bool
	Total  int
	Failed int
	// Detail is what the CLI said, trimmed. Kept whether or not it passed, because "0 tests" and
	// "3 tests, all green" are both worth reading back later.
	Detail string
	// FailureStage is the earliest stage a failing test stopped at, in the CLI's words:
	// EXTRACTION, COMPILATION or RESULT. Empty when nothing failed.
	FailureStage string
}

// testOutcome is the subset of the CLI's per-test JSON that decides the verdict.
type testOutcome struct {
	Test               string        `json:"test"`
	Pass               bool          `json:"pass"`
	FailureStage       string        `json:"failureStage"`
	FailureDescription string        `json:"failureDescription"`
	Messages           []testMessage `json:"messages"`
}

type testMessage struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Position struct {
		FileName string `json:"fileName"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
	} `json:"position"`
}

// stageOrder is the order a test is taken through, and so the order in which it can stop.
var stageOrder = map[string]int{"EXTRACTION": 0, "COMPILATION": 1, "RESULT": 2}

// stageRank places a stage this does not recognize after every one it does.
func stageRank(stage string) int {
	if rank, ok := stageOrder[stage]; ok {
		return rank
	}
	return len(stageOrder)
}

// reason says why one test failed, from what the CLI reported for the stage it stopped at.
func (t testOutcome) reason() string {
	switch t.FailureStage {
	case "EXTRACTION":
		if t.FailureDescription == "" {
			return "the test code did not extract"
		}
		return "the test code did not extract: " + t.FailureDescription
	case "COMPILATION":
		for _, m := range t.Messages {
			if m.Severity != "ERROR" {
				continue
			}
			if m.Position.FileName == "" {
				return "it does not compile: " + m.Message
			}
			return fmt.Sprintf("it does not compile: %s:%d:%d: %s",
				filepath.Base(m.Position.FileName), m.Position.Line, m.Position.Column, m.Message)
		}
		return "it does not compile"
	case "RESULT":
		return "the results differ from the .expected"
	}
	return t.FailureDescription
}

// RunTests evaluates the query tests in dirs, and only those.
//
// `codeql test run` is the only thing that can say a query does what its author claimed: it
// compiles the query, runs it against a database the CLI builds from the fixture tree, and
// compares the output to the checked-in `.expected`. ABOM never forms that opinion itself.
func (r *Runner) RunTests(ctx context.Context, dirs []string) (TestResult, error) {
	if len(dirs) == 0 {
		return TestResult{}, fmt.Errorf("runner: test run needs a directory")
	}
	for _, dir := range dirs {
		if dir == "" {
			return TestResult{}, fmt.Errorf("runner: test run needs a directory")
		}
	}
	args := append([]string{"test", "run"}, dirs...)
	args = append(args, localPackArgs()...)
	// The CLI deletes a passing test's database, and the AST step is read from it.
	args = append(args, "--format=json", "--keep-databases")
	if r.Threads > 0 {
		args = append(args, fmt.Sprintf("--threads=%d", r.Threads))
	}
	if r.RAMMB > 0 {
		args = append(args, fmt.Sprintf("--ram=%d", r.RAMMB))
	}

	release, err := r.Lock.Acquire(ctx)
	if err != nil {
		return TestResult{}, err
	}
	defer release()

	cmd := exec.CommandContext(ctx, r.codeql(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	// The exit status is not the verdict: a failing test is a non-zero exit and so is a broken
	// invocation, and only the first is an answer about the query. The JSON decides, and a
	// non-zero exit with no parsable output is the error case.
	result, parseErr := parseTestOutput(stdout.Bytes())
	if parseErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		named := strings.Join(dirs, ", ")
		if runErr != nil {
			return TestResult{}, fmt.Errorf("codeql test run %s: %w: %s", named, runErr, msg)
		}
		return TestResult{}, fmt.Errorf("codeql test run %s: %w", named, parseErr)
	}
	if result.Total == 0 {
		result.Detail = "no tests found, so nothing was shown to work"
	}
	return result, nil
}

// parseTestOutput reads the CLI's per-test JSON.
//
// A stream of objects rather than one array on some versions, so it is decoded as a stream and
// both shapes work.
func parseTestOutput(out []byte) (TestResult, error) {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return TestResult{}, fmt.Errorf("the CLI printed no test results")
	}

	var outcomes []testOutcome
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return TestResult{}, fmt.Errorf("the CLI printed no test results: %w", err)
		}
		if raw[0] == '[' {
			var batch []testOutcome
			if err := json.Unmarshal(raw, &batch); err != nil {
				return TestResult{}, fmt.Errorf("reading the CLI's test results: %w", err)
			}
			outcomes = append(outcomes, batch...)
			continue
		}
		var one testOutcome
		if err := json.Unmarshal(raw, &one); err != nil {
			return TestResult{}, fmt.Errorf("reading the CLI's test results: %w", err)
		}
		outcomes = append(outcomes, one)
	}
	// Valid JSON naming no test is a real answer: an empty directory. A parse failure is not.
	if len(outcomes) == 0 {
		return TestResult{}, nil
	}

	var result TestResult
	var first *testOutcome
	for i, t := range outcomes {
		result.Total++
		if t.Pass {
			continue
		}
		result.Failed++
		if first == nil || stageRank(t.FailureStage) < stageRank(first.FailureStage) {
			first = &outcomes[i]
		}
	}
	result.Passed = result.Failed == 0
	result.Detail = fmt.Sprintf("%d of %d passed", result.Total-result.Failed, result.Total)
	if first != nil {
		result.FailureStage = first.FailureStage
		if reason := first.reason(); reason != "" {
			result.Detail += "; " + reason
		}
	}
	return result, nil
}
