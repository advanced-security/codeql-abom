package serve

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Configuring in the interface is runtime seeding: the same declaration the command line writes
// before a crawl, written while one is stopped.
func TestTheDeclaredScopeCanBeWrittenAndReadBack(t *testing.T) {
	srv, _ := newServer(t)

	rec := put(t, srv, "/api/owned-scope", `[
	  {"owner":"acme","kind":"organization","enabled":true},
	  {"owner":"acme-labs","kind":"organization","enabled":true,
	   "includesOnly":true,"includes":["tooling"]}
	]`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	got := decode[[]store.DeclaredAccount](t, get(t, srv, "/api/owned-scope"))
	if len(got) != 2 {
		t.Fatalf("want both accounts, got %+v", got)
	}
	for _, a := range got {
		if a.Owner == "acme-labs" && (!a.IncludesOnly || len(a.Includes) != 1) {
			t.Errorf("acme-labs = %+v, want it restricted to one repository", a)
		}
	}
}

// An empty declaration is a real instruction — it stops discovery — so it has to be writable
// rather than being mistaken for a malformed request.
func TestTheDeclaredScopeCanBeEmptied(t *testing.T) {
	srv, _ := newServer(t)
	put(t, srv, "/api/owned-scope", `[{"owner":"acme","kind":"organization","enabled":true}]`)

	if rec := put(t, srv, "/api/owned-scope", `[]`); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := decode[[]store.DeclaredAccount](t, get(t, srv, "/api/owned-scope")); len(got) != 0 {
		t.Errorf("want nothing declared, got %+v", got)
	}
}

// A kind that is neither a user nor an organization is a client mistake, and answering 200 to it
// would leave the operator believing they had declared something.
func TestAnUnknownAccountKindIsRejected(t *testing.T) {
	srv, _ := newServer(t)

	rec := put(t, srv, "/api/owned-scope", `[{"owner":"acme","kind":"team","enabled":true}]`)

	if rec.Code == http.StatusOK {
		t.Error("a kind the store refuses must not be answered with success")
	}
}

func TestMalformedScopeIsRejected(t *testing.T) {
	srv, _ := newServer(t)

	if rec := put(t, srv, "/api/owned-scope", `not json`); rec.Code == http.StatusOK {
		t.Error("a body that is not a declaration must not be answered with success")
	}
}

// Declaring is the cheap direction and still worth stating: an operator who declares an account
// the corpus holds nothing from needs to know that now, not after a crawl that finds nothing.
func TestThePreviewNamesWhatWouldEnterTheClosure(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	got := preview(t, srv, `[{"owner":"octo-org","kind":"organization","enabled":true}]`)

	if got.RootsNow != 0 || got.RootsNext != 1 {
		t.Errorf("roots %d -> %d, want 0 -> 1", got.RootsNow, got.RootsNext)
	}
	// octo-org/app names actions/checkout, so the closure is wider than the declaration.
	if got.ClosureNext != 2 {
		t.Errorf("closure = %d, want the repository and what it reaches", got.ClosureNext)
	}
	if !slices.Contains(got.Entering, "octo-org/app") {
		t.Errorf("entering = %v, want the declared repository named", got.Entering)
	}
	if len(got.Leaving) != 0 {
		t.Errorf("leaving = %v, want nothing leaving when nothing was declared", got.Leaving)
	}
}

// Undeclaring is how a corpus silently shrinks, so what it costs is stated before the write
// rather than discovered afterwards on a page that has quietly emptied.
func TestThePreviewNamesWhatWouldLeaveTheClosure(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	put(t, srv, "/api/owned-scope", `[{"owner":"octo-org","kind":"organization","enabled":true}]`)

	got := preview(t, srv, `[]`)

	if got.RootsNow != 1 || got.RootsNext != 0 {
		t.Errorf("roots %d -> %d, want 1 -> 0", got.RootsNow, got.RootsNext)
	}
	if !slices.Contains(got.Leaving, "octo-org/app") {
		t.Errorf("leaving = %v, want the repository that stops being worked named", got.Leaving)
	}
	if len(got.Entering) != 0 {
		t.Errorf("entering = %v, want nothing entering when the scope only shrinks", got.Entering)
	}
}

// A preview an operator is deciding with must not be the decision. Writing on the way to
// answering would make canceling impossible and every preview a commitment.
func TestThePreviewLeavesTheDeclarationAlone(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	put(t, srv, "/api/owned-scope", `[{"owner":"octo-org","kind":"organization","enabled":true}]`)

	preview(t, srv, `[{"owner":"someone-else","kind":"organization","enabled":true}]`)

	got := decode[[]store.DeclaredAccount](t, get(t, srv, "/api/owned-scope"))
	if len(got) != 1 || got[0].Owner != "octo-org" {
		t.Errorf("declaration = %+v, want the preview to have changed nothing", got)
	}
	if declared, err := s.OwnedScopeDeclared(); err != nil || !declared {
		t.Errorf("OwnedScopeDeclared = %v (%v), want the stored scope untouched", declared, err)
	}
}

func preview(t *testing.T, srv *Server, body string) ScopePreview {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/owned-scope/preview", strings.NewReader(body))
	srv.Handler().ServeHTTP(rec, req)
	return decode[ScopePreview](t, rec)
}
