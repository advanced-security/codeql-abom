package serve

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json: %s", ct, rec.Body.String())
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem: %v: %s", err, rec.Body.String())
	}
	if p.Status != rec.Code || p.Type != "urn:abom:problem:"+string(p.Code) || p.Title == "" || p.Detail == "" {
		t.Errorf("problem %+v does not describe the %d it answered", p, rec.Code)
	}
	return p
}

// A refusal says what kind it is in a code a client can switch on, and in a sentence a person can
// read, in one shape whatever refused it: the guard, a handler, or a route nobody registered.
func TestEveryRefusalIsAProblemWithACode(t *testing.T) {
	srv, _ := newServer(t)
	for _, c := range []struct {
		r    *http.Request
		code string
	}{
		{localRequest(http.MethodGet, "/api/frontier?nonsense=1", nil), "unknown-parameter"},
		{localRequest(http.MethodGet, "/api/no-such-route", nil), "not-found"},
		{localRequest(http.MethodPost, "/api/discovery/pause", nil), "control-header-missing"},
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, c.r)
		if p := problemOf(t, rec); string(p.Code) != c.code {
			t.Errorf("%s %s answered %q, want %q", c.r.Method, c.r.URL, p.Code, c.code)
		}
	}
}

// Without a session, the refusal says where to sign in.
func TestASignInRefusalSaysWhereToSignIn(t *testing.T) {
	srv, _, _ := controlServer(t, "")
	srv.Access = signedIn
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, "/api/frontier", nil))
	if p := problemOf(t, rec); p.Code != "sign-in-required" || p.Login != "/auth/login" {
		t.Errorf("problem = %+v, want sign-in-required naming /auth/login", p)
	}
}

// A failure inside the server is said in its own words, not under the Go package that raised it.
func TestAServerFailureNamesNoGoPackage(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, nil, errors.New("store: reading the frontier: database is locked"))
	if p := problemOf(t, rec); p.Code != "internal" || p.Detail != "reading the frontier: database is locked" {
		t.Errorf("problem = %+v", p)
	}
}

// The codes are a closed list, each with one status: a call naming a code outside it, or a code
// nothing names, fails here rather than reaching a client that cannot switch on it.
func TestEveryProblemNamesAListedCodeAndEveryCodeIsUsed(t *testing.T) {
	listed := map[string]problemCode{} // constant name -> code, as problems.go declares them
	tree, err := parser.ParseFile(token.NewFileSet(), "problems.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range tree.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, spec := range g.Specs {
			v := spec.(*ast.ValueSpec)
			for i, name := range v.Names {
				if lit, ok := v.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					code, _ := strconv.Unquote(lit.Value)
					listed[name.Name] = problemCode(code)
				}
			}
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	used := map[problemCode]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			at, named := map[string]int{"writeProblem": 1, "newProblem": 0}[func() string {
				if ok {
					return id.Name
				}
				return ""
			}()]
			// problems.go answers writeError's statuses with codes chosen from the list itself.
			if !named || len(call.Args) <= at || file == "problems.go" {
				return true
			}
			name, ok := call.Args[at].(*ast.Ident)
			code, known := listed[name.Name]
			if !ok || !known {
				t.Errorf("%s: writeProblem names its code with something other than a constant problems.go lists", file)
				return true
			}
			used[code] = true
			return true
		})
	}
	for code, p := range problems {
		if p.status == 0 || p.title == "" {
			t.Errorf("%s has no status or title", code)
		}
		if !used[code] && !p.generic {
			t.Errorf("%s is listed and nothing answers it", code)
		}
	}
	for name, code := range listed {
		if _, ok := problems[code]; !ok {
			t.Errorf("%s is %q, which has no entry in the list", name, code)
		}
	}
}
