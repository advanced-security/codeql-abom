package serve

import (
	"context"
	"encoding/json"
	"net/http"

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
	RunTests(ctx context.Context, dir string) (runner.TestResult, error)
}

// queries answers the catalog: every query version the ledger has run, and the stage somebody
// put it in.
func (s *Server) queries(w http.ResponseWriter, r *http.Request) {
	catalog, err := s.Store.QueryCatalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"queries": catalog, "stages": store.Stages()}, nil)
}

// testQuery runs a version's tests and records what the CLI said.
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
	case s.Tester == nil || s.TestDir == "":
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

	result, err := s.Tester.RunTests(r.Context(), s.TestDir)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.Store.RecordQueryTest(store.QueryTest{
		QueryID: body.QueryID, QueryVersion: body.Version,
		Passed: result.Passed, Total: result.Total, Failed: result.Failed, Detail: result.Detail,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeCatalog(w)
}

func (s *Server) writeCatalog(w http.ResponseWriter) {
	catalog, err := s.Store.QueryCatalog()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"queries": catalog, "stages": store.Stages()}, nil)
}

// advanceQuery records that somebody moved a version to a stage.
//
// A write, because readiness is computed and advancement is not. Refusing an unknown stage here
// rather than storing it: a version in a state nothing downstream can read is cheapest to catch
// at the boundary. `test` is refused unless that version's tests are on record and green, which
// is what makes one rung of the ladder earned rather than pressed.
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
