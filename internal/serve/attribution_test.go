package serve

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/access"
	"github.com/advanced-security/codeql-abom/internal/authz"
)

// With sign-in on, who filed a request is the person signed in, whatever name the body carries:
// anyone signed in could otherwise file one as somebody else.
func TestWithSignInTheSignedInPersonIsWhoFiledARequest(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.Access = people{"admin": {ID: "https://github.com#1", Username: "ada", Role: authz.RoleAdmin}}
	r := localRequest(http.MethodPost, "/api/query-requests", strings.NewReader(
		`{"description":"flag a self-hosted runner","compliant":"runs-on: ubuntu-latest",
		  "nonCompliant":"runs-on: self-hosted","requestedBy":"mallory"}`))
	r.Header.Set(controlHeader, "1")

	if w := serveAs(s, "admin", r); w.Code != http.StatusOK {
		t.Fatalf("filing a request answered %d: %s", w.Code, w.Body.String())
	}

	if got := readRequests(t, s); len(got.Requests) != 1 || got.Requests[0].RequestedBy != "ada" {
		t.Errorf("requests = %+v; want one filed by ada", got.Requests)
	}
}

// Without sign-in nobody signed in to say otherwise, so the name the interface sent is kept.
func TestWithoutSignInTheNameSentIsWhoFiledARequest(t *testing.T) {
	s, _, _ := controlServer(t, "")
	r := localRequest(http.MethodPost, "/api/query-requests", strings.NewReader(aRequestBody))
	r.Header.Set(controlHeader, "1")

	if w := serveAs(s, "", r); w.Code != http.StatusOK {
		t.Fatalf("filing a request answered %d: %s", w.Code, w.Body.String())
	}

	if got := readRequests(t, s); len(got.Requests) != 1 || got.Requests[0].RequestedBy != "operator" {
		t.Errorf("requests = %+v; want one filed by operator", got.Requests)
	}
}

func TestTheActorIsTheSignedInPersonWhenThereIsOne(t *testing.T) {
	s, _ := newServer(t)
	r := localRequest(http.MethodPost, "/api/runs", nil)
	if got := s.actor(r, "mallory"); got != "mallory" {
		t.Errorf("without sign-in, actor = %q; want the name sent", got)
	}
	r = r.WithContext(withUser(r.Context(), access.User{ID: "https://github.com#1", Username: "ada"}))
	if got := s.actor(r, "mallory"); got != "ada" {
		t.Errorf("signed in, actor = %q; want ada", got)
	}
}

// claimedActors are the request fields in which the interface names who is acting.
var claimedActors = map[string]bool{"By": true, "RequestedBy": true, "GrantedBy": true}

// Every handler replaces the name a body claims with s.actor before anything reads it, so no path
// can record a name the person signed in did not hold. Checked over the source rather than path
// by path, because the next handler that takes a `by` is the one nobody remembers to test.
func TestNoHandlerReadsAClaimedActorBeforeTheActorReplacesIt(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			replaced := map[string]token.Pos{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
					return true
				}
				field, ok := bodyField(as.Lhs[0])
				if call, isCall := as.Rhs[0].(*ast.CallExpr); ok && isCall && isActorCall(call) {
					replaced[field] = as.End()
				}
				return true
			})
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && isActorCall(call) {
					return false
				}
				if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 1 {
					if _, ok := bodyField(as.Lhs[0]); ok {
						for _, rhs := range as.Rhs {
							ast.Inspect(rhs, func(m ast.Node) bool { return checkRead(t, fset, fn, replaced, m) })
						}
						return false
					}
				}
				return checkRead(t, fset, fn, replaced, n)
			})
		}
	}
}

func checkRead(t *testing.T, fset *token.FileSet, fn *ast.FuncDecl, replaced map[string]token.Pos, n ast.Node) bool {
	t.Helper()
	if call, ok := n.(*ast.CallExpr); ok && isActorCall(call) {
		return false
	}
	field, ok := bodyField(n)
	if !ok {
		return true
	}
	if at, done := replaced[field]; !done || n.Pos() < at {
		t.Errorf("%s: %s reads body.%s before replacing it with s.actor(r, body.%s)",
			fset.Position(n.Pos()), fn.Name.Name, field, field)
	}
	return false
}

// bodyField names the claimed-actor field n selects from the decoded body, if it does.
func bodyField(n ast.Node) (string, bool) {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok || !claimedActors[sel.Sel.Name] {
		return "", false
	}
	if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "body" {
		return "", false
	}
	return sel.Sel.Name, true
}

func isActorCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "actor" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "s"
}
