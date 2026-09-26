package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The surfaces the interface reads a named scope's refusal for: every Inventory unit, the graph
// and a run.
var pageSurfaces = []string{
	"inventory", "workflows", "jobs", "repositories", "advisories", "relationships", "coverage",
	"graph", "run",
}

func namedViews(t *testing.T, srv *Server) []NamedView {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/views", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/views answered %d: %s", rec.Code, rec.Body.String())
	}
	var views []NamedView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	if len(views) == 0 {
		t.Fatal("the catalog is empty, so this proves nothing")
	}
	return views
}

// A scope is kept wherever it was built and opened wherever it is offered, so every page that
// offers one is told what it cannot apply, including when that is nothing.
func TestTheCatalogSaysWhatEveryPageCannotApply(t *testing.T) {
	srv, _ := unitServer(t)

	for _, v := range namedViews(t, srv) {
		for _, surface := range pageSurfaces {
			refused, ok := v.Refuses[surface]
			if !ok || refused == nil {
				t.Errorf("%s says nothing about %s", v.Name, surface)
			}
		}
		// Every built-in is a question the Actions unit answers: they are offered there first.
		if v.Builtin && len(v.Refuses["inventory"]) > 0 {
			t.Errorf("built-in %q is offered on the Actions unit and names %v, which it refuses",
				v.Name, v.Refuses["inventory"])
		}
	}
}

// A saved graph question is disabled where it cannot be drawn and offered where it can, rather
// than refused on every unit because one of them cannot apply it.
func TestASavedGraphQuestionNamesTheTermOnlyWhereItIsRefused(t *testing.T) {
	srv, db := unitServer(t)
	if err := db.SaveView(store.SavedView{
		Name: "crossings", Predicate: "boundary=cross-owner",
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	for _, v := range namedViews(t, srv) {
		if v.Name != "crossings" {
			continue
		}
		for surface, want := range map[string][]string{
			"inventory":     {"boundary"},
			"coverage":      {"boundary"},
			"run":           {"boundary"},
			"relationships": {},
			"graph":         {},
		} {
			if got := v.Refuses[surface]; !slices.Equal(got, want) {
				t.Errorf("on %s it names %v, want %v", surface, got, want)
			}
		}
		return
	}
	t.Fatal("the saved view is not in the catalog at all")
}

// What the catalog says a page refuses is what that page's own endpoint refuses. A chip enabled by
// one answer and refused by the other is the refusal that arrives a click too late.
func TestWhatTheCatalogSaysAPageRefusesIsWhatItsEndpointRefuses(t *testing.T) {
	srv, db := unitServer(t)
	// Kept scopes that only some pages apply, beside the built-ins every one of which is offered
	// on the Actions unit.
	for name, predicate := range map[string]string{
		"crossings":      "boundary=cross-owner",
		"needs":          "edgeKinds=job-needs-job",
		"upstream-rules": "source=upstream",
		"unpinned-rule":  "rules=actions/unpinned-tag",
	} {
		if err := db.SaveView(store.SavedView{Name: name, Predicate: predicate}); err != nil {
			t.Fatalf("SaveView %s: %v", name, err)
		}
	}
	endpoints := map[string]string{
		"inventory":     "/api/actions?",
		"workflows":     "/api/workflows?",
		"jobs":          "/api/jobs?",
		"repositories":  "/api/repositories?",
		"advisories":    "/api/remediation?",
		"relationships": "/api/relationships?",
		"coverage":      "/api/frontier?",
		"graph":         "/api/graph/full?",
		"run":           "/api/databases?",
	}

	compared := 0
	for _, v := range namedViews(t, srv) {
		for surface, path := range endpoints {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+v.Predicate, nil))
			refused := v.Refuses[surface]
			switch {
			case len(refused) == 0 && rec.Code != http.StatusOK:
				t.Errorf("%s is offered on %s, whose endpoint answers %d: %s",
					v.Name, surface, rec.Code, rec.Body.String())
			case len(refused) > 0 && rec.Code != http.StatusBadRequest:
				t.Errorf("%s is disabled on %s for %v, whose endpoint answers %d",
					v.Name, surface, refused, rec.Code)
			case len(refused) > 0:
				// A route refuses at the first parameter it does not declare, so it names one.
				if !slices.ContainsFunc(refused, func(term string) bool {
					return strings.Contains(rec.Body.String(), term)
				}) {
					t.Errorf("%s on %s: the endpoint's refusal %q names none of %v",
						v.Name, surface, rec.Body.String(), refused)
				}
			}
			compared++
		}
	}
	if compared == 0 {
		t.Fatal("nothing was compared")
	}
}
