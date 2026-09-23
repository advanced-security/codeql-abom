package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// TestResult is what `codeql test run` made of one directory of query tests.
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
}

// testOutcome is the subset of the CLI's per-test JSON that decides the verdict.
type testOutcome struct {
	Test string `json:"test"`
	Pass bool   `json:"pass"`
}

// RunTests evaluates the query tests under dir.
//
// `codeql test run` is the only thing that can say a query does what its author claimed: it
// compiles the query, runs it against a database the CLI builds from the fixture tree, and
// compares the output to the checked-in `.expected`. ABOM never forms that opinion itself.
func (r *Runner) RunTests(ctx context.Context, dir string) (TestResult, error) {
	if dir == "" {
		return TestResult{}, fmt.Errorf("runner: test run needs a directory")
	}
	args := append([]string{"test", "run", dir}, localPackArgs()...)
	args = append(args, "--format=json")
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
		if runErr != nil {
			return TestResult{}, fmt.Errorf("codeql test run %s: %w: %s", dir, runErr, msg)
		}
		return TestResult{}, fmt.Errorf("codeql test run %s: %w", dir, parseErr)
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

	var result TestResult
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	for {
		var batch []testOutcome
		if err := dec.Decode(&batch); err == nil {
			for _, t := range batch {
				result.Total++
				if !t.Pass {
					result.Failed++
				}
			}
			continue
		}
		break
	}
	if result.Total > 0 {
		result.Passed = result.Failed == 0
		result.Detail = fmt.Sprintf("%d of %d passed", result.Total-result.Failed, result.Total)
		return result, nil
	}

	// Fall back to a stream of single objects.
	dec = json.NewDecoder(bytes.NewReader(trimmed))
	for {
		var one testOutcome
		if err := dec.Decode(&one); err != nil {
			break
		}
		result.Total++
		if !one.Pass {
			result.Failed++
		}
	}
	if result.Total == 0 {
		// Valid JSON naming no test is a real answer: an empty directory. A parse failure is not.
		if bytes.Equal(trimmed, []byte("[]")) {
			return TestResult{}, nil
		}
		return TestResult{}, fmt.Errorf("the CLI printed no test results")
	}
	result.Passed = result.Failed == 0
	result.Detail = fmt.Sprintf("%d of %d passed", result.Total-result.Failed, result.Total)
	return result, nil
}
