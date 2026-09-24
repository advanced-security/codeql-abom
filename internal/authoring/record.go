package authoring

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// ErrChangedDuringTests is a query or its tests changing while they ran, so the result describes
// neither version.
var ErrChangedDuringTests = errors.New("changed while the tests ran")

// ErrTestsDidNotRun is the CLI failing to carry out a test run, which is no answer about the query.
var ErrTestsDidNotRun = errors.New("the tests could not be run")

// Tests runs a query's own tests.
type Tests func(ctx context.Context, tests []string) (runner.TestResult, error)

// RecordTests runs a query version's own tests and records what the CLI said against the version,
// its tests, its fixtures and the CodeQL that ran them.
//
// ABOM never forms the opinion itself: `codeql test run` compiles the query, runs it against a
// database built from the fixture tree and compares the output to the checked-in `.expected`.
func RecordTests(ctx context.Context, st *store.Store, run Tests, root string, q Query, cli string) (runner.TestResult, error) {
	digest, err := DigestTests(root, q.Tests)
	if err != nil {
		return runner.TestResult{}, err
	}
	fixtures, err := DigestFixtures(root, q.Tests)
	if err != nil {
		return runner.TestResult{}, err
	}

	// Not handed to the CLI, which would read no test as every test it could find.
	result := runner.TestResult{
		Detail: fmt.Sprintf("no test names %s, so nothing was shown to work", q.Rel),
	}
	if len(q.Tests) > 0 {
		if result, err = run(ctx, q.Tests); err != nil {
			return runner.TestResult{}, fmt.Errorf("%w: %w", ErrTestsDidNotRun, err)
		}
	}
	if changed, err := changedSince(root, q, digest); err != nil || changed {
		if err == nil {
			err = fmt.Errorf("%s or its tests %w, so the result describes neither version and was not "+
				"recorded; run them again", q.ID, ErrChangedDuringTests)
		}
		return runner.TestResult{}, err
	}

	if err := st.RecordQueryTest(store.QueryTest{
		QueryID: q.ID, QueryVersion: q.Version, CLIVersion: cli,
		Passed: result.Passed, Total: result.Total, Failed: result.Failed, Detail: result.Detail,
		FailureStage: result.FailureStage, TestDigest: digest, FixturesDigest: fixtures,
		Missing: result.Missing, Unexpected: result.Unexpected,
	}); err != nil {
		return runner.TestResult{}, err
	}
	return result, nil
}

// changedSince reports whether a query or its tests are no longer what they were before a run.
func changedSince(root string, before Query, digest string) (bool, error) {
	now, found, err := Find(root, before.ID)
	if err != nil || !found {
		return true, err
	}
	nowDigest, err := DigestTests(root, now.Tests)
	if err != nil {
		return true, err
	}
	return now.Version != before.Version || nowDigest != digest ||
		!slices.Equal(now.Tests, before.Tests), nil
}
