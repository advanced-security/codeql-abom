package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// QueryTester runs the query tests in a directory.
//
// An interface so the server does not depend on the CodeQL plumbing. Nil means this server was
// started without a custom query directory, which is a thing the reader is told rather than a
// fault.
type QueryTester interface {
	RunTests(ctx context.Context, tests []string) (runner.TestResult, error)
}

// queries answers the catalog: every query version the ledger has run, the version on disk in the
// operator's pack, and the stage somebody put each in.
func (s *Server) queries(w http.ResponseWriter, _ *http.Request) {
	s.writeCatalog(w)
}

// testQuery runs a version's own tests and records what the CLI said.
//
// ABOM never forms the opinion itself: `codeql test run` compiles the query, runs it against a
// database built from the fixture tree and compares the output to the checked-in `.expected`.
// This records that verdict, and `AdvanceQueryStage` is what reads it.
func (s *Server) testQuery(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	var body struct {
		QueryID string `json:"queryId"`
		Version string `json:"queryVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	switch {
	case s.Tester == nil || s.QueryDir == "":
		writeError(w, http.StatusServiceUnavailable,
			"this server has no query directory, so there are no tests to run; "+
				"start it with --custom-ql-dir")
		return
	case body.QueryID == "" || body.Version == "":
		// Refused rather than recorded against an empty version: a result that belongs to nothing
		// would be found later for a version it never described.
		writeError(w, http.StatusBadRequest, "a test result belongs to a query version")
		return
	}

	q, found, err := authoring.Find(s.QueryDir, body.QueryID)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	case !found:
		writeError(w, http.StatusNotFound, fmt.Sprintf(
			"%s is not a query in the query directory, so ABOM has no tests to run for it",
			body.QueryID))
		return
	case q.Version != body.Version:
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"%s has changed on disk since this version was listed; reload to test what is there now",
			body.QueryID))
		return
	}
	digest, err := authoring.DigestTests(s.QueryDir, q.Tests)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Not handed to the CLI, which would read no test as every test it could find.
	result := runner.TestResult{
		Detail: fmt.Sprintf("no test names %s, so nothing was shown to work", q.Rel),
	}
	if len(q.Tests) > 0 {
		if result, err = s.Tester.RunTests(r.Context(), q.Tests); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	if changed, err := s.changedSince(q, digest); err != nil || changed {
		msg := fmt.Sprintf("%s or its tests changed while the tests ran, so the result describes "+
			"neither version and was not recorded; run them again", body.QueryID)
		if err != nil {
			msg = err.Error()
		}
		writeError(w, http.StatusConflict, msg)
		return
	}

	if err := s.Store.RecordQueryTest(store.QueryTest{
		QueryID: body.QueryID, QueryVersion: body.Version,
		Passed: result.Passed, Total: result.Total, Failed: result.Failed, Detail: result.Detail,
		FailureStage: result.FailureStage, TestDigest: digest,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeCatalog(w)
}

// changedSince reports whether a query or its tests are no longer what they were before a run.
func (s *Server) changedSince(before authoring.Query, digest string) (bool, error) {
	now, found, err := authoring.Find(s.QueryDir, before.ID)
	if err != nil || !found {
		return true, err
	}
	nowDigest, err := authoring.DigestTests(s.QueryDir, now.Tests)
	if err != nil {
		return true, err
	}
	return now.Version != before.Version || nowDigest != digest ||
		!slices.Equal(now.Tests, before.Tests), nil
}

// catalogRow is a catalog entry with the steps its working copy has taken. Every other row carries
// none: ABOM holds no tests for a version that is not on disk in the operator's pack.
type catalogRow struct {
	store.CatalogQuery
	Steps []authoring.Step `json:"steps"`
}

func (s *Server) writeCatalog(w http.ResponseWriter) {
	working, steps, err := s.workingCopy()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	catalog, err := s.Store.QueryCatalog(working...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]catalogRow, 0, len(catalog))
	for _, q := range catalog {
		row := catalogRow{CatalogQuery: q, Steps: []authoring.Step{}}
		if q.Working {
			row.Steps = append(row.Steps, steps[q.QueryID+"\x00"+q.QueryVersion]...)
		}
		rows = append(rows, row)
	}
	writeJSON(w, map[string]any{"queries": rows, "stages": store.Stages()}, nil)
}

// workingCopy is what the operator's pack holds on disk now, which the store cannot read, and the
// steps each query there has taken.
func (s *Server) workingCopy() ([]store.WorkingQuery, map[string][]authoring.Step, error) {
	steps := map[string][]authoring.Step{}
	if s.QueryDir == "" {
		return nil, steps, nil
	}
	queries, err := authoring.Workspace(s.QueryDir)
	if err != nil {
		return nil, nil, err
	}
	printed, err := s.Store.NewestASTs()
	if err != nil {
		return nil, nil, err
	}
	out := make([]store.WorkingQuery, 0, len(queries))
	for _, q := range queries {
		pack, _, _ := runner.PackOf(q.Path)
		out = append(out, store.WorkingQuery{
			QueryID: q.ID, QueryVersion: q.Version, Pack: pack, Kind: q.Kind,
		})
		tests, err := s.Store.QueryTestsOf(q.ID)
		if err != nil {
			return nil, nil, err
		}
		progress, err := authoring.Progress(s.QueryDir, q, tests, printed[q.ID])
		if err != nil {
			return nil, nil, err
		}
		steps[q.ID+"\x00"+q.Version] = progress
	}
	return out, steps, nil
}

// advanceQuery records that somebody moved a version to a stage.
//
// A write, because readiness is computed and advancement is not. Refusing an unknown stage here
// rather than storing it: a version in a state nothing downstream can read is cheapest to catch
// at the boundary. `test` is refused unless that version's tests are on record and green, and
// `extended-test` unless every step of the version on disk is passing, which is what makes the
// first two rungs of the ladder earned rather than pressed.
func (s *Server) advanceQuery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		QueryID string `json:"queryId"`
		Version string `json:"queryVersion"`
		Stage   string `json:"stage"`
		By      string `json:"by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	if body.Stage == store.StageExtendedTest {
		refusal, err := s.extendedTestRefusal(body.QueryID, body.Version)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if refusal != "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("advancing %s to %s: %s",
				body.QueryID, store.StageExtendedTest, refusal))
			return
		}
	}
	if err := s.Store.AdvanceQueryStage(body.QueryID, body.Version, body.Stage, body.By); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The deployed suite is a projection of what is at the deploy stage, so it is rewritten on
	// every advance rather than only on one: taking a query back out has to remove it too.
	if err := s.writeDeployedSuite(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeCatalog(w)
}

// extendedTestRefusal says why a version may not enter extended-test, or is empty when it may.
//
// Here rather than in the store, because the steps are read from files the store cannot see.
func (s *Server) extendedTestRefusal(id, version string) (string, error) {
	if s.QueryDir == "" {
		return "this server has no query directory, so none of its steps can be shown", nil
	}
	q, found, err := authoring.Find(s.QueryDir, id)
	switch {
	case err != nil:
		return "", err
	case !found:
		return "it is not in the query directory, so none of its steps can be shown", nil
	case q.Version != version:
		return "only the version on disk has steps to show, and this is not it", nil
	}
	tests, err := s.Store.QueryTestsOf(id)
	if err != nil {
		return "", err
	}
	printed, err := s.Store.NewestASTs()
	if err != nil {
		return "", err
	}
	steps, err := authoring.Progress(s.QueryDir, q, tests, printed[id])
	if err != nil {
		return "", err
	}
	return authoring.ExtendedTestRefusal(steps), nil
}

// writeDeployedSuite rewrites the suite a tier binds from the query ids at the deploy stage.
//
// Does nothing when no query directory is configured: there is no pack to hold a suite, and
// deploying is then a stage somebody recorded rather than a thing that runs.
func (s *Server) writeDeployedSuite() error {
	dir := runner.CustomPackRoot()
	if dir == "" {
		return nil
	}
	deployed, err := s.Store.DeployedQueryIDs()
	if err != nil {
		return err
	}
	return customql.WriteDeployed(dir, deployed)
}
