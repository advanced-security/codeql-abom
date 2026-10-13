package demo

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/expand"
)

func provided(t *testing.T) (Workspace, *httptest.Server) {
	t.Helper()
	ws := initialized(t)
	srv := httptest.NewServer((&Provider{Layout: ws.Layout, Manifest: ws.Manifest}).Handler())
	t.Cleanup(srv.Close)
	return ws, srv
}

// The checkout and the API client are the production readers; the provider must satisfy them as
// GitHub would for the fictional repositories, so a run builds a real database from them.
func TestTheProductionReadersReadTheDemoRepositoriesFromTheProvider(t *testing.T) {
	ws, srv := provided(t)
	want, _ := ws.Manifest.Has("acme-corp/payments")
	checkout := &expand.Checkout{BaseURL: srv.URL + "/git"}

	tree, err := checkout.Fetch(t.Context(), filepath.Join(t.TempDir(), "head"), "acme-corp", "payments", "")
	if err != nil {
		t.Fatalf("Fetch default branch: %v", err)
	}
	if tree.SHA != want.Commit {
		t.Fatalf("fetched %s, the manifest names %s", tree.SHA, want.Commit)
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, ".github", "workflows", "ci.yml")); err != nil {
		t.Fatalf("the workflow was not materialized: %v", err)
	}
	pinned, err := checkout.Fetch(t.Context(), filepath.Join(t.TempDir(), "pinned"), "acme-corp", "payments", want.Commit)
	if err != nil || pinned.SHA != want.Commit {
		t.Fatalf("Fetch at the commit: %+v, %v", pinned, err)
	}

	client := catalog.New(srv.URL, "", srv.Client())
	head, err := client.HeadSHA(t.Context(), "acme-corp/payments")
	if err != nil || head != want.Commit {
		t.Fatalf("HeadSHA = %q, %v; want %s", head, err, want.Commit)
	}
	moved, err := client.CompareCommits(t.Context(), "acme-corp/payments", head, head)
	if err != nil || moved.Status != "identical" {
		t.Fatalf("CompareCommits = %+v, %v", moved, err)
	}
	// Fictional repositories have never been scanned; saying otherwise would invent alerts.
	if _, err := client.CodeScanningAlerts(t.Context(), "acme-corp/payments"); !errors.Is(err, catalog.ErrNoAnalysis) {
		t.Fatalf("CodeScanningAlerts: %v, want no analysis", err)
	}
}

// A name that happens to match a real repository must never be served or fetched from anywhere.
func TestTheProviderServesNothingOutsideTheDataset(t *testing.T) {
	_, srv := provided(t)
	checkout := &expand.Checkout{BaseURL: srv.URL + "/git"}
	if _, err := checkout.Fetch(t.Context(), t.TempDir(), "actions", "checkout", ""); err == nil {
		t.Fatal("a repository outside the dataset was fetched")
	}
	if _, err := catalog.New(srv.URL, "", srv.Client()).HeadSHA(t.Context(), "actions/checkout"); err == nil {
		t.Fatal("a repository outside the dataset has a head")
	}
	for _, path := range []string{"/user", "/graphql", "/orgs/acme-corp/repos", "/repos/acme-corp/payments/zipball"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s answered %d, want 404", path, resp.StatusCode)
		}
	}
}

// The provider is read-only: a push, a GraphQL mutation or a REST write is refused before git runs.
func TestTheProviderRefusesEveryWrite(t *testing.T) {
	_, srv := provided(t)
	for _, req := range []struct{ method, path string }{
		{"GET", "/git/acme-corp/payments.git/info/refs?service=git-receive-pack"},
		{"POST", "/git/acme-corp/payments.git/git-receive-pack"},
		{"POST", "/graphql"},
		{"POST", "/repos/acme-corp/payments/issues"},
		{"PATCH", "/repos/acme-corp/payments"},
		{"PUT", "/repos/acme-corp/payments/contents/README.md"},
	} {
		r, _ := http.NewRequest(req.method, srv.URL+req.path, strings.NewReader("{}"))
		resp, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 400 || resp.StatusCode >= 500 {
			t.Errorf("%s %s answered %d %s, want a refusal", req.method, req.path, resp.StatusCode, body)
		}
	}
}

// Loopback is the boundary: a forwarded or rebound name must not reach practice repositories.
func TestTheProviderAnswersOnlyLoopbackNames(t *testing.T) {
	_, srv := provided(t)
	r, _ := http.NewRequest("GET", srv.URL+"/repos/acme-corp/payments", nil)
	r.Host = "abom-demo.app.github.dev"
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a request addressed to %s answered %d", r.Host, resp.StatusCode)
	}
}
