package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// fakePrinter answers like PrintAST, and keeps what it was asked to print.
type fakePrinter struct {
	asked [][]string
}

func (f *fakePrinter) PrintAST(_ context.Context, database string, sources []string) (runner.PrintedTree, error) {
	f.asked = append(f.asked, append([]string{database}, sources...))
	return runner.PrintedTree{Body: "#    1| [WorkflowImpl] name: ci\n"}, nil
}

func (f *fakePrinter) Version(context.Context) (string, error) { return fakeCLI, nil }

// printServer serves a pack holding acme/a, whose test database was extracted after its fixtures.
func printServer(t *testing.T) (*Server, *store.Store, *fakePrinter, string) {
	t.Helper()
	s, st := newServer(t)
	root := queryWorkspace(t, []string{"A"})
	yml := filepath.Join(root, "test", "A", "A.testproj", "codeql-database.yml")
	if err := os.MkdirAll(filepath.Dir(yml), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yml, []byte("---\nprimaryLanguage: actions\nfinalised: true\n"), 0o644); err != nil { //nolint:misspell // the CLI's key
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(yml, later, later); err != nil {
		t.Fatal(err)
	}
	printer := &fakePrinter{}
	s.Tester, s.Printer, s.QueryDir = &fakeTester{}, printer, root
	return s, st, printer, root
}

func printTree(t *testing.T, s *Server, version, by string, control bool) (int, string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"queryId": "acme/a", "queryVersion": version, "by": by})
	if err != nil {
		t.Fatal(err)
	}
	w := sessionPost(t, s, "/api/queries/ast", string(body), control)
	return w.Code, w.Body.String()
}

// A query nobody drove a session over still earns its AST step: ABOM prints the tree itself, over
// the test database it keeps, and says who asked.
func TestABOMPrintsAQuerysTreeItselfAndItCountsForTheASTStep(t *testing.T) {
	s, st, printer, root := printServer(t)
	code, body := printTree(t, s, versionOf(t, root, "A"), "operator", true)
	if code != http.StatusOK {
		t.Fatalf("printing answered %d: %s", code, body)
	}
	if len(printer.asked) != 1 || !strings.HasSuffix(printer.asked[0][0], filepath.Join("test", "A", "A.testproj")) ||
		strings.Join(printer.asked[0][1:], ",") != ".github/workflows/ci.yml" {
		t.Errorf("asked to print %v", printer.asked)
	}
	trees, err := st.QueryASTs("acme/a")
	if err != nil || len(trees) != 1 || trees[0].PrintedBy != "operator" || trees[0].SessionID != 0 ||
		trees[0].Database != "test/A/A.testproj" || trees[0].SourceFiles != ".github/workflows/ci.yml" {
		t.Errorf("kept %+v %v", trees, err)
	}
	var catalog struct {
		Queries []catalogRow `json:"queries"`
	}
	if err := json.Unmarshal([]byte(body), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, q := range catalog.Queries {
		for _, step := range q.Steps {
			if q.QueryID == "acme/a" && step.Step == "ast" && step.State != "passing" {
				t.Errorf("the AST step after printing reads %+v", step)
			}
		}
	}
	var listed struct {
		Asts []store.AuthoringAST `json:"asts"`
	}
	if code := sessionGet(t, s, "/api/queries/asts?query=acme%2Fa", &listed); code != http.StatusOK ||
		len(listed.Asts) != 1 || listed.Asts[0].PrintedBy != "operator" {
		t.Errorf("the query's trees answered %d with %+v", code, listed.Asts)
	}
}

// A tree is printed over a database that holds the fixtures as they are, at a person's asking,
// when nothing else holds CodeQL; otherwise it would describe code that is gone.
func TestPrintingIsRefusedWhereTheTreeWouldNotDescribeTheFixtures(t *testing.T) {
	s, _, printer, root := printServer(t)
	version := versionOf(t, root, "A")
	if code, _ := printTree(t, s, version, "operator", false); code != http.StatusForbidden {
		t.Errorf("printing from another origin answered %d, want 403", code)
	}
	if code, _ := printTree(t, s, version, " ", true); code != http.StatusBadRequest {
		t.Errorf("printing for nobody answered %d, want 400", code)
	}
	if code, _ := printTree(t, s, "not-this-version", "operator", true); code != http.StatusConflict {
		t.Errorf("printing a version not on disk answered %d, want 409", code)
	}
	s.Discovery = &crawling{state: supervisor.StateWorking}
	if code, _ := printTree(t, s, version, "operator", true); code != http.StatusConflict {
		t.Errorf("printing while discovery runs answered %d, want 409", code)
	}
	s.Discovery = nil
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(root, "test", "A", ".github", "workflows", "ci.yml"), later, later); err != nil {
		t.Fatal(err)
	}
	code, body := printTree(t, s, version, "operator", true)
	if code != http.StatusConflict || !strings.Contains(body, "older than") {
		t.Errorf("printing over a database older than its fixtures answered %d: %s", code, body)
	}
	if err := os.RemoveAll(filepath.Join(root, "test", "A", "A.testproj")); err != nil {
		t.Fatal(err)
	}
	code, body = printTree(t, s, version, "operator", true)
	if code != http.StatusConflict || !strings.Contains(body, "extracted") {
		t.Errorf("printing with no database answered %d: %s", code, body)
	}
	if len(printer.asked) != 0 {
		t.Errorf("printed %v", printer.asked)
	}
}
