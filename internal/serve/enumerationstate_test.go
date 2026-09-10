package serve

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func readScope(t *testing.T, srv *Server) []declaredAccountView {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/owned-scope", nil))
	var out []declaredAccountView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return out
}

// A repository deleted after the last full listing is still reported until the next one. That is
// correct and it is only defensible if a reader can see how long ago the listing was — otherwise
// the staleness has to be inferred from nothing.
func TestTheScopeSaysWhenAnAccountWasLastReadAndHow(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")

	before := readScope(t, srv)
	if len(before) != 1 {
		t.Fatalf("scope holds %d accounts, want 1", len(before))
	}
	if !before[0].Enumeration.ListedAt.IsZero() || before[0].Enumeration.Route != "" {
		t.Errorf("an unread account reported %+v, want nothing established", before[0].Enumeration)
	}

	srv.Enumerate = fakeEnumerator(map[string][]string{
		"advanced-security": {"advanced-security/one"},
	})
	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	after := readScope(t, srv)
	if after[0].Enumeration.ListedAt.IsZero() {
		t.Error("a full listing has run, so when it ran has to reach the reader")
	}
	if after[0].Enumeration.Route != catalog.RouteGraphQL {
		t.Errorf("route %q, want the route that answered to be stated",
			after[0].Enumeration.Route)
	}
}

// Which route answered is a hidden variable in every figure downstream unless it is reported. The
// two are meant to agree; when they do not, only an attributable figure is diagnosable.
func TestASweptAccountIsNotReportedAsFullyListed(t *testing.T) {
	srv, st := newServer(t)
	declare(t, st, "advanced-security")
	mark := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	if err := st.RecordFullListing("advanced-security", catalog.RouteREST, 3, mark, mark); err != nil {
		t.Fatal(err)
	}
	listed := readScope(t, srv)[0].Enumeration.ListedAt

	srv.Enumerate = func(context.Context, string, catalog.Marks) (catalog.Listing, error) {
		return catalog.Listing{Route: catalog.RouteGraphQL, Full: false}, nil
	}
	var report EnumerationReport
	postJSON(t, srv, "/api/owned-scope/enumerate", &report)

	after := readScope(t, srv)[0].Enumeration
	if !after.ListedAt.Equal(listed) {
		t.Errorf("the sweep moved the full-listing time from %v to %v", listed, after.ListedAt)
	}
	if after.SweptAt.IsZero() {
		t.Error("the sweep ran, and when it ran is the other half of the answer")
	}
}

// Failing to read the marks must not empty the declaration: an account whose enumeration state
// cannot be read is still an account the operator declared.
func TestAnAccountWithNoMarksIsStillDeclared(t *testing.T) {
	srv, st := newServer(t)
	if err := st.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "advanced-security", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}

	got := readScope(t, srv)
	if len(got) != 1 || got[0].Owner != "advanced-security" {
		t.Errorf("scope = %+v, want the declaration regardless of its marks", got)
	}
}
