package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// controlServer is a server whose decision key directory is known, so its log can be verified.
func controlServer(t *testing.T, authzDir string) (*Server, *store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abom.db")
	if authzDir == "" {
		authzDir = store.DefaultAuthzDir(path)
	}
	s, err := store.Open(path, store.Options{BulkLoad: true, AuthzDir: authzDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return &Server{Store: s}, s, authzDir
}

// decisionStore is somewhere for a server's controls to be decided, for a test about something else.
func decisionStore(t *testing.T) *store.Store {
	t.Helper()
	_, s, _ := controlServer(t, "")
	return s
}

func newServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, s, _ := controlServer(t, "")
	return srv, s
}

func localRequest(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	// httptest's implicit example.com Host does not represent a local ABOM client.
	if strings.HasPrefix(target, "/") {
		r.Host = "127.0.0.1:8080"
	}
	return r
}

func controlRequest(target string) *http.Request {
	r := localRequest(http.MethodPost, target, nil)
	r.Host = "127.0.0.1:8080"
	r.Header.Set(controlHeader, "1")
	return r
}

// control sends a control request carrying what one needs, as the interface on loopback does.
func control(t *testing.T, srv *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := localRequest(method, target, strings.NewReader(body))
	r.Header.Set(controlHeader, "1")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, localRequest(http.MethodGet, path, nil))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	return out
}
