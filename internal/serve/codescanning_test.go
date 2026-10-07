package serve

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/setup"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type readingsBody struct {
	Counts   map[string]int `json:"counts"`
	Declared int            `json:"declared"`
	Read     int            `json:"read"`
	Items    []struct {
		Repository      string   `json:"repository"`
		Class           string   `json:"class"`
		ActionsScanning string   `json:"actionsScanning"`
		Setup           string   `json:"setup"`
		ActionsDatabase string   `json:"actionsDatabase"`
		Languages       []string `json:"languages"`
		Evidence        string   `json:"evidence"`
		Writable        bool     `json:"writable"`
	} `json:"items"`
}

func TestCodeScanningReadingsListEveryClassWithItsEvidence(t *testing.T) {
	srv, s := newServer(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"octo/covered", "octo/unread"} {
		if _, err := s.InsertOwnedRepository(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordSetupReading(store.SetupReading{Repository: "octo/covered",
		ReadAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), State: "configured",
		Languages: []string{"actions"}}); err != nil {
		t.Fatal(err)
	}

	got := decode[readingsBody](t, get(t, srv, "/api/code-scanning/readings"))
	if got.Counts[store.SetupCovered] != 1 || got.Counts[store.SetupNotRead] != 1 || got.Declared != 2 || got.Read != 1 {
		t.Errorf("readings %+v", got)
	}
	if len(got.Items) != 2 {
		t.Fatalf("listed %d repositories, want 2", len(got.Items))
	}
	for _, item := range got.Items {
		if item.Evidence == "" || item.Languages == nil || item.ActionsScanning == "" {
			t.Errorf("%s answered without its evidence, languages or columns: %+v", item.Repository, item)
		}
	}

	narrowed := decode[readingsBody](t, get(t, srv, "/api/code-scanning/readings?class=covered"))
	if len(narrowed.Items) != 1 || narrowed.Items[0].Repository != "octo/covered" {
		t.Errorf("class=covered listed %+v", narrowed.Items)
	}
	summary := decode[readingsBody](t, get(t, srv, "/api/code-scanning/readings?summary=true"))
	if len(summary.Items) != 0 || summary.Counts[store.SetupCovered] != 1 || summary.Declared != 2 {
		t.Errorf("summary=true answered %d items and counts %v, want the counts alone", len(summary.Items), summary.Counts)
	}
	if rec := get(t, srv, "/api/code-scanning/readings?class=sideways"); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown class answered %d, want 400 rather than an empty list", rec.Code)
	}
	if rec := get(t, srv, "/api/code-scanning/readings?top=3"); rec.Code != http.StatusBadRequest {
		t.Errorf("a parameter the endpoint does not apply answered %d, want 400", rec.Code)
	}
}

func writesServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, s := newServer(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"octo/app", "octo/lib"} {
		if _, err := s.InsertOwnedRepository(r); err != nil {
			t.Fatal(err)
		}
	}
	srv.CodeScanningWrites = &setup.Service{Store: s}
	return srv, s
}

func TestAPreviewIsTheViewsRepositoriesAndChangesNothing(t *testing.T) {
	srv, s := writesServer(t)
	if err := s.SaveView(store.SavedView{Name: "just-app", Predicate: "q=octo%2Fapp", CreatedBy: store.DeclaredByCLI}); err != nil {
		t.Fatal(err)
	}
	rec := control(t, srv, "/api/code-scanning/previews", `{"view":"just-app","exclude":[]}`)
	if strings.Contains(rec.Body.String(), `"rows"`) {
		t.Errorf("a preview answered with its rows, %s; at organization scale that is every repository", rec.Body.String())
	}
	got := decode[struct {
		ID       int64          `json:"id"`
		ViewName string         `json:"viewName"`
		Counts   map[string]int `json:"counts"`
	}](t, rec)
	if got.ID == 0 || got.ViewName != "just-app" || got.Counts[store.SetupNotRead] != 1 {
		t.Errorf("preview %+v, want the view's one repository counted", got)
	}
	rows := decode[struct {
		Items []struct {
			Repository string `json:"repository"`
		} `json:"items"`
		Total int `json:"total"`
	}](t, get(t, srv, fmt.Sprintf("/api/code-scanning/previews/%d/rows?top=30&offset=0", got.ID)))
	if rows.Total != 1 || len(rows.Items) != 1 || rows.Items[0].Repository != "octo/app" {
		t.Errorf("the preview's rows paged as %+v, want octo/app of 1", rows)
	}
	if rec := get(t, srv, fmt.Sprintf("/api/code-scanning/previews/%d/rows?sort=class", got.ID)); rec.Code != http.StatusBadRequest {
		t.Errorf("a parameter the rows do not apply answered %d, want 400", rec.Code)
	}
	if rec := get(t, srv, fmt.Sprintf("/api/code-scanning/previews/%d/rows", got.ID+1)); rec.Code != http.StatusNotFound {
		t.Errorf("the rows of a preview that does not exist answered %d, want 404", rec.Code)
	}
	if rec := control(t, srv, "/api/code-scanning/previews", `{"view":"nowhere"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown view answered %d, want 400", rec.Code)
	}
	if rec := get(t, srv, "/api/code-scanning/previews"); rec.Code != http.StatusOK {
		t.Errorf("listing previews answered %d", rec.Code)
	}
}

func TestWritingIsControlledAndRefusedWhereTheServerWritesNothing(t *testing.T) {
	srv, _ := writesServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/code-scanning/previews", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden {
		t.Errorf("a preview without the control header answered %d, want 403", rec.Code)
	}
	created := decode[struct {
		ID int64 `json:"id"`
	}](t, control(t, srv, "/api/code-scanning/previews", `{}`))
	if rec := control(t, srv, fmt.Sprintf("/api/code-scanning/previews/%d/approve", created.ID), ``); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "--enable-targets") {
		t.Errorf("approving on a server that writes nothing answered %d %s", rec.Code, rec.Body.String())
	}
	writes := decode[struct {
		Writes bool `json:"writes"`
	}](t, get(t, srv, "/api/code-scanning/writes"))
	if writes.Writes {
		t.Error("a server with no targets says it writes")
	}
	if rec := get(t, srv, "/api/code-scanning/ledger?top=50"); rec.Code != http.StatusOK ||
		strings.TrimSpace(rec.Body.String()) != `{"items":[],"total":0}` {
		t.Errorf("an empty ledger answered %d %q, want no steps of none", rec.Code, rec.Body.String())
	}
}
