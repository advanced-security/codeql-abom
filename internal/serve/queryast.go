package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// ASTPrinter prints the tree of fixtures in a test database, and says which CodeQL printed it.
type ASTPrinter interface {
	PrintAST(ctx context.Context, database string, sourceFiles []string) (runner.PrintedTree, error)
	Version(ctx context.Context) (string, error)
}

type printQueryASTRequest struct {
	QueryID string `json:"queryId"`
	Version string `json:"queryVersion"`
	By      string `json:"by"`
}

type queryASTPrintPlan struct {
	request  printQueryASTRequest
	query    authoring.Query
	database string
	fixtures []string
	digest   string
}

// printQueryAST prints a query's tree over its test database and keeps it, as a session would,
// naming who asked in place of a session. A query nobody drove a session over earns its AST step
// this way.
func (s *Server) printQueryAST(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	body, ok := s.decodePrintQueryAST(w, r)
	if !ok {
		return
	}
	if msg := s.discoveryHoldsCodeQL("printing a tree"); msg != "" {
		writeError(w, http.StatusConflict, msg)
		return
	}
	q, ok := s.findPrintQuery(w, body)
	if !ok {
		return
	}
	plan, ok := s.queryASTPrintPlan(w, body, q)
	if !ok {
		return
	}
	if !s.printAndRecordQueryAST(w, r, plan) {
		return
	}
	s.writeCatalog(w)
}

func (s *Server) decodePrintQueryAST(w http.ResponseWriter, r *http.Request) (printQueryASTRequest, bool) {
	var body printQueryASTRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return printQueryASTRequest{}, false
	}
	switch {
	case s.Printer == nil || s.QueryDir == "":
		writeError(w, http.StatusServiceUnavailable, "this server has no query directory to print a tree "+
			"from; start it with --custom-ql-dir")
		return printQueryASTRequest{}, false
	case strings.TrimSpace(body.By) == "":
		writeError(w, http.StatusBadRequest, "a tree ABOM prints records who asked for it; sign in with gh")
		return printQueryASTRequest{}, false
	case body.QueryID == "" || body.Version == "":
		writeError(w, http.StatusBadRequest, "a tree is printed for a query version")
		return printQueryASTRequest{}, false
	default:
		return body, true
	}
}

func (s *Server) findPrintQuery(w http.ResponseWriter, body printQueryASTRequest) (authoring.Query, bool) {
	q, found, err := authoring.Find(s.QueryDir, body.QueryID)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return authoring.Query{}, false
	case !found:
		writeError(w, http.StatusNotFound, body.QueryID+" is not a query in the query directory")
		return authoring.Query{}, false
	case q.Version != body.Version:
		writeError(w, http.StatusConflict, fmt.Sprintf("%s has changed on disk since this version was "+
			"listed; reload to print what is there now", body.QueryID))
		return authoring.Query{}, false
	default:
		return q, true
	}
}

func (s *Server) queryASTPrintPlan(
	w http.ResponseWriter,
	body printQueryASTRequest,
	q authoring.Query,
) (queryASTPrintPlan, bool) {
	database, fixtures, why, err := authoring.Printable(s.QueryDir, q)
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return queryASTPrintPlan{}, false
	case why != "":
		writeError(w, http.StatusConflict, why)
		return queryASTPrintPlan{}, false
	}
	digest, err := authoring.DigestFixtures(s.QueryDir, q.Tests)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return queryASTPrintPlan{}, false
	}
	return queryASTPrintPlan{
		request: body, query: q, database: database, fixtures: fixtures, digest: digest,
	}, true
}

func (s *Server) printAndRecordQueryAST(
	w http.ResponseWriter,
	r *http.Request,
	plan queryASTPrintPlan,
) bool {
	cli, err := s.Printer.Version(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return false
	}
	printedAt := time.Now().UTC()
	tree, err := s.Printer.PrintAST(r.Context(), plan.database, plan.fixtures)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return false
	}
	if !s.fixturesStillMatch(w, plan) {
		return false
	}
	rel, err := filepath.Rel(s.QueryDir, plan.database)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	if _, err := s.Store.RecordAuthoringAST(store.AuthoringAST{
		PrintedBy: plan.request.By, QueryID: plan.query.ID,
		PrintedAt: printedAt.Format(time.RFC3339Nano), Query: "PrintAST",
		Database: filepath.ToSlash(rel), SourceFiles: strings.Join(plan.fixtures, ","),
		TestDigest: plan.digest, Body: tree.Body, Truncated: tree.Truncated, CLIVersion: cli,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return false
	}
	return true
}

func (s *Server) fixturesStillMatch(w http.ResponseWriter, plan queryASTPrintPlan) bool {
	if now, err := authoring.DigestFixtures(s.QueryDir, plan.query.Tests); err != nil || now != plan.digest {
		writeError(w, http.StatusConflict, "the fixtures changed while the tree was printed, so it "+
			"describes neither and was not kept; print it again")
		return false
	}
	return true
}

// queryASTs lists the trees printed for a query, by a session or by ABOM, without the trees.
func (s *Server) queryASTs(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("query")
	if id == "" {
		writeError(w, http.StatusBadRequest, "name the query whose trees to list")
		return
	}
	asts, err := s.Store.QueryASTs(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"asts": asts}, nil)
}
