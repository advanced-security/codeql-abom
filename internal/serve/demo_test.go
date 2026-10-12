package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// servedPatterns reads every route serve.go registers, so a route added later is covered here
// without anyone remembering to list it.
func servedPatterns(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+ /api/[^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	if len(out) < 100 {
		t.Fatalf("read only %d routes from serve.go; the pattern no longer matches how routes are registered", len(out))
	}
	return out
}

var pathParameter = regexp.MustCompile(`\{[a-z]+(\.\.\.)?\}`)

func demoServer(t *testing.T) *Server {
	srv, _ := newServer(t)
	srv.Instance = Instance{Mode: ModeDemo, Dataset: "demo-test", Scenario: "corpus-1",
		Authoring: "unavailable", Repositories: "simulated"}
	return srv
}

func demoRefused(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusForbidden && rec.Header().Get("X-Abom-Refused-By") == "demo"
}

// Deny by default: a Demo instance runs only the practice it names, so a control added to the
// server later is refused there until someone decides it belongs in practice.
func TestDemoAdmitsOnlyThePracticeControlsItNames(t *testing.T) {
	srv := demoServer(t)
	patterns := servedPatterns(t)
	for control := range demoControls {
		if !slices.Contains(patterns, control) {
			t.Errorf("the Demo allowlist names %q, which is not served", control)
		}
	}
	handler := srv.Handler()
	for _, pattern := range patterns {
		method, path, _ := strings.Cut(pattern, " ")
		if method == http.MethodGet {
			continue
		}
		r := localRequest(method, pathParameter.ReplaceAllString(path, "1"), strings.NewReader("{}"))
		r.Header.Set("X-Abom-Control", "1")
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if want := !demoControls[pattern]; demoRefused(rec) != want {
			t.Errorf("%s: refused by Demo = %v, want %v (%d %s)", pattern, demoRefused(rec), want,
				rec.Code, rec.Body.String())
		}
	}
}

// Demo never helps share modified practice data, and a GET download is as much an export as a POST.
func TestDemoWithholdsDownloadsButNotTheReadsBehindThem(t *testing.T) {
	handler := demoServer(t).Handler()
	for path, refused := range map[string]bool{
		"/api/graph/export":                       true,
		"/api/graph/export?format=json":           true,
		"/api/bom?format=cyclonedx&download=true": true,
		"/api/node/acme-corp%2Fci/-/closure.svg":  true,
		"/api/node/acme-corp%2Fci/-/closure":      false,
		"/api/bom?format=summary":                 false,
		"/api/summary":                            false,
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, localRequest(http.MethodGet, path, nil))
		if demoRefused(rec) != refused {
			t.Errorf("GET %s: refused by Demo = %v, want %v", path, demoRefused(rec), refused)
		}
	}
}

// An ordinary server is untouched by Demo's policy.
func TestAnOrdinaryServerIsNotSubjectToDemoPolicy(t *testing.T) {
	srv, _ := newServer(t)
	handler := srv.Handler()
	for _, req := range []*http.Request{
		localRequest(http.MethodGet, "/api/graph/export", nil),
		localRequest(http.MethodPost, "/api/discovery/start", nil),
	} {
		req.Header.Set("X-Abom-Control", "1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if demoRefused(rec) {
			t.Errorf("%s %s was refused by Demo policy on an ordinary server", req.Method, req.URL)
		}
	}
}

// The interface labels an instance from what the server says it is, never from browser state.
func TestTheInstanceSaysWhatTheServerIs(t *testing.T) {
	ordinary, _ := newServer(t)
	if got := decode[Instance](t, get(t, ordinary, "/api/instance")); got.Mode != ModeNormal || got.Dataset != "" {
		t.Errorf("an ordinary server says %+v", got)
	}
	rec := get(t, demoServer(t), "/api/instance")
	got := decode[Instance](t, rec)
	if got.Mode != ModeDemo || got.Dataset != "demo-test" || got.Repositories != "simulated" {
		t.Errorf("a Demo server says %+v", got)
	}
	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	for key := range raw {
		if strings.Contains(strings.ToLower(key), "path") || strings.Contains(strings.ToLower(key), "dir") {
			t.Errorf("the instance description exposes %q", key)
		}
	}
}

// A Codespace forwards the interface under one exact name; admitting it must not admit any other.
func TestOnlyTheExactForwardedHostIsAdmitted(t *testing.T) {
	srv, _ := newServer(t)
	srv.AllowedHosts = []string{"abom-demo-8099.app.github.dev"}
	handler := srv.Handler()
	for host, want := range map[string]int{
		"abom-demo-8099.app.github.dev":      http.StatusOK,
		"ABOM-DEMO-8099.app.github.dev":      http.StatusOK,
		"other-8099.app.github.dev":          http.StatusForbidden,
		"abom-demo-8099.app.github.dev.evil": http.StatusForbidden,
		"127.0.0.1:8099":                     http.StatusOK,
	} {
		r := localRequest(http.MethodGet, "/api/summary", nil)
		r.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("Host %s answered %d, want %d", host, rec.Code, want)
		}
	}
}
