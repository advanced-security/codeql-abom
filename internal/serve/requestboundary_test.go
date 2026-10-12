package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func boundaryRequest(method, path, body string, controlled bool) *http.Request {
	r := localRequest(method, path, strings.NewReader(body))
	r.Host = "127.0.0.1:8080"
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	if controlled {
		r.Header.Set(controlHeader, "1")
	}
	return r
}

func TestHTTPBoundaryRejectsReboundHostsBeforeReadingGitHub(t *testing.T) {
	for _, host := range []string{"attacker.example:8080", "localhost.attacker.example", "127.0.0.1.attacker.example"} {
		t.Run(host, func(t *testing.T) {
			s, files := filesServer(t)
			body, err := json.Marshal(store.RequestExample{URL: ciLink})
			if err != nil {
				t.Fatal(err)
			}
			r := boundaryRequest(http.MethodPost, "/api/query-requests/example", string(body), true)
			r.Host = host
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.Header.Set("X-Forwarded-Host", "localhost")
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("rebound request answered %d: %s", w.Code, w.Body.String())
			}
			if len(files.refCalls) != 0 || len(files.fileCalls) != 0 {
				t.Errorf("untrusted request reached GitHub: refs=%v files=%v", files.refCalls, files.fileCalls)
			}
		})
	}
}

func TestHTTPBoundaryProtectsReadsOnCoordinatorsReadersAndReplicas(t *testing.T) {
	for _, mode := range []string{"coordinator", "reader", "replica"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newServer(t)
			s.Diagnostics = true
			s.ReadOnly = mode == "reader"
			if mode == "replica" {
				s.Replica, _ = newServer(t)
			}
			for _, path := range []string{"/api/summary", "/api/query-requests", "/api/events/github", "/debug/pprof/cmdline"} {
				r := boundaryRequest(http.MethodGet, path, "", false)
				r.Host = "attacker.example:8080"
				r.Header.Set("Sec-Fetch-Site", "same-origin")
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Errorf("%s answered %d to an untrusted Host", path, w.Code)
				}
			}
		})
	}
}

func TestHTTPBoundaryKeepsLocalReadsHeaderFree(t *testing.T) {
	s, _ := newServer(t)
	for _, host := range []string{"localhost", "LOCALHOST:8080", "127.0.0.1:8080", "127.0.0.2", "[::1]:8080"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			r := boundaryRequest(method, "/api/summary", "", false)
			r.Host = host
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("%s %s answered %d: %s", method, host, w.Code, w.Body.String())
			}
		}
	}
}

func TestHTTPBoundaryPreservesExplicitRemoteControlWithoutRelaxingReaders(t *testing.T) {
	s, _ := newServer(t)
	s.AllowRemoteControl = true
	r := boundaryRequest(http.MethodGet, "/api/summary", "", false)
	r.Host = "abom.internal.example"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("explicitly allowed remote read answered %d: %s", w.Code, w.Body.String())
	}
	for _, site := range []string{"", "cross-site"} {
		r = boundaryRequest(http.MethodPost, "/api/queries/stage", "{}", site != "")
		r.Host = "abom.internal.example"
		r.Header.Set("Sec-Fetch-Site", site)
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("remote opt-in bypassed control checks: %d %s", w.Code, w.Body.String())
		}
	}
	s.ReadOnly = true
	r = boundaryRequest(http.MethodPost, "/api/queries/stage", "{}", true)
	r.Host = "abom.internal.example"
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "only serves reads") {
		t.Errorf("remote opt-in allowed a reader to write: %d %s", w.Code, w.Body.String())
	}
}

func TestHTTPBoundaryKeepsReadOnlyPreviewsHeaderFreeButHostBound(t *testing.T) {
	s, _ := newServer(t)
	s.SnapshotPreview = func(string) (SnapshotPreview, error) { return SnapshotPreview{}, nil }
	for _, path := range []string{"/api/owned-scope/preview", "/api/repositories/octo/app/snapshot/preview"} {
		for _, host := range []string{"127.0.0.1", "attacker.example"} {
			r := boundaryRequest(http.MethodPost, path, "[]", false)
			r.Host = host
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			want := http.StatusOK
			if host == "attacker.example" {
				want = http.StatusForbidden
			}
			if w.Code != want {
				t.Errorf("%s at %s answered %d, want %d: %s", path, host, w.Code, want, w.Body.String())
			}
		}
	}
}

func TestHTTPBoundaryExemptsOnlyAuthenticatedWebhookDeliveries(t *testing.T) {
	s, st, _ := receiving(t)
	for _, valid := range []bool{false, true} {
		r := boundaryRequest(http.MethodPost, "/api/events/github", pushBody, false)
		r.Host = "webhooks.example"
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("X-GitHub-Delivery", "boundary-delivery")
		r.Header.Set("X-GitHub-Event", "push")
		secret := []byte("wrong secret")
		want := http.StatusUnauthorized
		if valid {
			secret, want = webhookSecret, http.StatusAccepted
		}
		r.Header.Set("X-Hub-Signature-256", signedWith(secret, []byte(pushBody)))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("webhook signature valid=%v answered %d, want %d: %s", valid, w.Code, want, w.Body.String())
		}
		count := 0
		if valid {
			count = 1
		}
		if got := received(t, st); got != count {
			t.Errorf("webhook signature valid=%v recorded %d deliveries, want %d", valid, got, count)
		}
	}
	s.ReadOnly = true
	r := boundaryRequest(http.MethodPost, "/api/events/github", pushBody, false)
	r.Host = "webhooks.example"
	r.Header.Set("X-Hub-Signature-256", signedWith(webhookSecret, []byte(pushBody)))
	r.Header.Set("X-GitHub-Delivery", "reader-delivery")
	r.Header.Set("X-GitHub-Event", "push")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || received(t, st) != 1 {
		t.Errorf("webhook exemption bypassed a reader's write refusal: %d %s", w.Code, w.Body.String())
	}
}

func registeredAPIRoutes(t *testing.T) []string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the routing source")
	}
	tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(file), "serve.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	ast.Inspect(tree, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandleFunc" {
			return true
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			t.Fatal("an HTTP route is not a literal the boundary test can enumerate")
		}
		pattern, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(pattern, " /api/") {
			routes = append(routes, pattern)
		}
		return true
	})
	if len(routes) == 0 {
		t.Fatal("the boundary test found no API routes")
	}
	return routes
}

func TestHTTPBoundaryGuardsEveryAPIMutation(t *testing.T) {
	s, _ := newServer(t)
	s.Verify = func(context.Context) (ghauth.Identity, error) {
		return ghauth.Identity{}, errors.New("an untrusted request reached the identity reader")
	}
	for _, pattern := range registeredAPIRoutes(t) {
		method, path, _ := strings.Cut(pattern, " ")
		if method == http.MethodGet || method == http.MethodHead ||
			pattern == "POST /api/events/github" || pattern == "POST /api/owned-scope/preview" ||
			pattern == "POST /api/repositories/{owner}/{name}/snapshot/preview" {
			continue
		}
		t.Run(pattern, func(t *testing.T) {
			parts := strings.Split(path, "/")
			for i, part := range parts {
				if strings.HasPrefix(part, "{") {
					parts[i] = "1"
				}
			}
			r := boundaryRequest(method, strings.Join(parts, "/"), "{}", false)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), controlHeader) {
				t.Errorf("unguarded mutation answered %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHTTPBoundaryRejectsCrossSiteLifecycleWritesWithoutChangingState(t *testing.T) {
	for _, site := range []string{"", "cross-site", "same-site"} {
		t.Run(site, func(t *testing.T) {
			s, st := newServer(t)
			s.QueryDir = t.TempDir()
			body := `{"queryId":"~remote-choice\n- exclude:\n    id: /.*/","queryVersion":"","stage":"deploy","by":"operator"}`
			r := boundaryRequest(http.MethodPost, "/api/queries/stage", body, site != "")
			r.Header.Set("Content-Type", "text/plain")
			r.Header.Set("Sec-Fetch-Site", site)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("untrusted lifecycle request answered %d: %s", w.Code, w.Body.String())
			}
			ids, err := st.DeployedQueryIDs()
			if err != nil || len(ids) != 0 {
				t.Errorf("refused request changed deployed IDs: %v, %v", ids, err)
			}
			_, err = os.Stat(filepath.Join(s.QueryDir, customql.DeploySuitePath))
			if !errors.Is(err, os.ErrNotExist) {
				t.Errorf("refused request touched the deployed suite: %v", err)
			}
		})
	}
}

func TestQueryLifecycleRejectsUnknownVersionsBeforeChangingState(t *testing.T) {
	for _, tc := range []struct{ id, version string }{
		{"~remote-choice\n- exclude:\n    id: /.*/", "v1"},
		{"acme/unknown", "v1"},
		{"acme/a", ""},
		{"acme/a", " "},
		{"acme/a", "unlisted-version"},
	} {
		t.Run(tc.id+"@"+tc.version, func(t *testing.T) {
			s, st := newServer(t)
			s.QueryDir = queryWorkspace(t, []string{"A"})
			s.Tester = &fakeTester{unreadable: errors.New("invalid input must not probe CodeQL")}
			if err := customql.WriteDeployed(s.QueryDir, nil); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.QueryDir, customql.DeploySuitePath)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(map[string]string{
				"queryId": tc.id, "queryVersion": tc.version, "stage": store.StageDeploy, "by": "operator",
			})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, boundaryRequest(http.MethodPost, "/api/queries/stage", string(body), true))
			if w.Code != http.StatusBadRequest {
				t.Errorf("unlisted version answered %d: %s", w.Code, w.Body.String())
			}
			ids, err := st.DeployedQueryIDs()
			if err != nil || len(ids) != 0 {
				t.Errorf("refused request changed deployed IDs: %v, %v", ids, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Errorf("refused request changed its suite: %v", err)
			}
		})
	}
}

func TestQueryLifecycleDeploysAndWithdrawsAKnownVersion(t *testing.T) {
	s, st := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})
	s.Tester = &fakeTester{}
	version := versionOf(t, s.QueryDir, "A")
	for _, stage := range []string{store.StageDeploy, store.StageDev} {
		body, err := json.Marshal(map[string]string{
			"queryId": "acme/a", "queryVersion": version, "stage": stage, "by": "operator",
		})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, boundaryRequest(http.MethodPost, "/api/queries/stage", string(body), true))
		if w.Code != http.StatusOK {
			t.Fatalf("valid %s request answered %d: %s", stage, w.Code, w.Body.String())
		}
		ids, err := st.DeployedQueryIDs()
		if err != nil {
			t.Fatal(err)
		}
		if stage == store.StageDeploy && (len(ids) != 1 || ids[0] != "acme/a") {
			t.Errorf("deployment recorded %v", ids)
		}
		if stage == store.StageDev && len(ids) != 0 {
			t.Errorf("withdrawal left queries deployed: %v", ids)
		}
	}
}
