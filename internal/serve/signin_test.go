package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/advanced-security/codeql-abom/internal/access"
	"github.com/advanced-security/codeql-abom/internal/authz"
)

// people stands in for the provider: each session cookie value names one signed-in person.
type people map[string]access.User

func (p people) User(r *http.Request) (access.User, bool) {
	c, err := r.Cookie(access.SessionCookie)
	if err != nil {
		return access.User{}, false
	}
	u, ok := p[c.Value]
	return u, ok
}
func (people) Login(w http.ResponseWriter, _ *http.Request)    { w.WriteHeader(http.StatusFound) }
func (people) Callback(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
func (people) Logout(w http.ResponseWriter, _ *http.Request)   { w.WriteHeader(http.StatusNoContent) }

var signedIn = people{
	"viewer":   {ID: "https://idp.example#s1", Username: "vera@example.com", Role: authz.RoleViewer},
	"operator": {ID: "https://idp.example#s2", Username: "otto@example.com", Role: authz.RoleOperator},
	"nobody":   {ID: "https://idp.example#s3", Username: "nora@example.com"},
}

func as(session string, r *http.Request) *http.Request {
	if session != "" {
		r.AddCookie(&http.Cookie{Name: access.SessionCookie, Value: session})
	}
	return r
}

func serveAs(s *Server, session string, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, as(session, r))
	return w
}

// With sign-in on, the API answers nobody who has not signed in, and says where to; signing in
// itself, and the webhook its own signature authenticates, stay reachable.
func TestASignedInDeploymentAnswersNobodyAnonymous(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.Access = signedIn

	w := serveAs(s, "", localRequest(http.MethodGet, "/api/summary", nil))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "/auth/login") {
		t.Errorf("an anonymous read answered %d: %s", w.Code, w.Body.String())
	}
	if w := serveAs(s, "", localRequest(http.MethodGet, "/auth/login", nil)); w.Code != http.StatusFound {
		t.Errorf("signing in answered %d", w.Code)
	}
	if w := serveAs(s, "", localRequest(http.MethodPost, "/api/events/github", strings.NewReader("{}"))); w.Code == http.StatusUnauthorized &&
		strings.Contains(w.Body.String(), "sign in") {
		t.Error("the webhook asked GitHub to sign in")
	}
	if w := serveAs(s, "viewer", localRequest(http.MethodGet, "/api/owned-scope", nil)); w.Code != http.StatusOK {
		t.Errorf("a viewer's read answered %d: %s", w.Code, w.Body.String())
	}
}

// Signed in is not the same as granted: a person ABOM's app roles say nothing about is told so.
func TestAPersonWithoutAnABOMRoleIsToldSo(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.Access = signedIn
	w := serveAs(s, "nobody", localRequest(http.MethodGet, "/api/summary", nil))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "role") {
		t.Errorf("a person with no role answered %d: %s", w.Code, w.Body.String())
	}
}

// A role below what an operation needs is refused, and the refusal is a signed decision naming the
// person: refused by a signed-in principal is part of the record, unlike a boundary refusal.
func TestARoleBelowAnOperationsIsRefusedAndTheRefusalSigned(t *testing.T) {
	s, st, dir := controlServer(t, "")
	s.Access = signedIn
	r := localRequest(http.MethodPost, "/api/discovery/start", nil)
	r.Header.Set(controlHeader, "1")

	w := serveAs(s, "viewer", r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), authz.RoleOperator) {
		t.Errorf("a viewer starting discovery answered %d: %s", w.Code, w.Body.String())
	}
	d, _ := headDecision(t, st, dir)
	if d.Allowed() || d.Operation() != authz.OpDiscoveryControl || d.Principal() != authz.SignedIn("https://idp.example#s1", "vera@example.com") {
		t.Errorf("logged %s allowed=%v by %+v; want Vera's refusal", d.Operation(), d.Allowed(), d.Principal())
	}
}

// Every decision names the person who asked, not the local principal.
func TestEveryControlNamesTheSignedInPerson(t *testing.T) {
	s, st, dir := controlServer(t, "")
	s.Access = signedIn
	r := localRequest(http.MethodPut, "/api/views/mine", strings.NewReader(`{"terms":"scope=owned"}`))
	r.Header.Set(controlHeader, "1")

	serveAs(s, "operator", r)
	d, _ := headDecision(t, st, dir)
	if !d.Allowed() || d.Principal() != authz.SignedIn("https://idp.example#s2", "otto@example.com") {
		t.Errorf("logged allowed=%v by %+v; want Otto's", d.Allowed(), d.Principal())
	}
}

// One request, one person, linked: the decision about a declaration's content names who asked and
// cites the route decision that admitted them, so the log reads as one act rather than two.
func TestAContentDecisionNamesWhoAskedAndCitesTheRouteDecision(t *testing.T) {
	s, st, dir := controlServer(t, "")
	s.Access = signedIn
	r := localRequest(http.MethodPut, "/api/owned-scope",
		strings.NewReader(`[{"owner":"acme","kind":"organization","enabled":true}]`))
	r.Header.Set(controlHeader, "1")

	serveAs(s, "operator", r)

	entries, err := st.Assertions(0, 0)
	if err != nil || len(entries) < 2 {
		t.Fatalf("logged %d entries, %v; want the route decision and the declaration's", len(entries), err)
	}
	bundle, err := authz.ReadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	route, err := bundle.DecisionOf(entries[len(entries)-2])
	if err != nil {
		t.Fatal(err)
	}
	content, err := bundle.DecisionOf(entries[len(entries)-1])
	if err != nil {
		t.Fatal(err)
	}
	otto := authz.SignedIn("https://idp.example#s2", "otto@example.com")
	if route.Subject() != authz.RouteSubject("PUT /api/owned-scope") || route.Principal() != otto {
		t.Fatalf("the route decision is %s by %+v", route.Subject(), route.Principal())
	}
	if content.Principal() != otto || !strings.Contains(string(content.Evidence()), `"route":"`+route.Digest()+`"`) {
		t.Errorf("the declaration's decision names %+v with evidence %s; want Otto, citing %s",
			content.Principal(), content.Evidence(), route.Digest())
	}
}

// The interface asks who is signed in, what their role allows and what each control needs, to say
// why a control is not offered rather than let it fail; without sign-in everything is allowed.
func TestMeSaysWhoIsSignedInAndWhatTheyMayDo(t *testing.T) {
	type me struct {
		Mode  string            `json:"mode"`
		User  *access.User      `json:"user"`
		May   []string          `json:"may"`
		Needs map[string]string `json:"needs"`
	}
	s, _, _ := controlServer(t, "")
	local := decode[me](t, get(t, s, "/api/me"))
	if local.Mode != "local" || local.User != nil || !slices.Contains(local.May, authz.OpConfigure) {
		t.Errorf("local /api/me = %+v", local)
	}

	s.Access = signedIn
	var viewer me
	if err := json.Unmarshal(serveAs(s, "viewer", localRequest(http.MethodGet, "/api/me", nil)).Body.Bytes(), &viewer); err != nil {
		t.Fatal(err)
	}
	if viewer.Mode != "signed-in" || viewer.User == nil || viewer.User.Username != "vera@example.com" ||
		!slices.Contains(viewer.May, authz.OpSavedView) || slices.Contains(viewer.May, authz.OpDiscoveryControl) {
		t.Errorf("a viewer's /api/me = %+v", viewer)
	}
	// What a control waits for is said by name, so the interface can say which role to ask for.
	need, _ := authz.Lookup(authz.OpDiscoveryControl)
	if viewer.Needs[authz.OpDiscoveryControl] != need.Role {
		t.Errorf("a viewer is told %q is needed for %s, want %q", viewer.Needs[authz.OpDiscoveryControl],
			authz.OpDiscoveryControl, need.Role)
	}
}

// With sign-in on, a session is needed for everything that reads what ABOM holds, not only /api/:
// the metrics name the declared accounts, and pprof serves memory. The interface's own files and
// the sign-in exchange stay public, because nothing could sign in without them.
func TestASignedInDeploymentGuardsEveryRouteButTheInterfaceAndSignIn(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.Access = signedIn
	s.UI = fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}
	s.Diagnostics = true
	for path, want := range map[string]int{
		"/metrics":       http.StatusUnauthorized,
		"/debug/pprof/":  http.StatusUnauthorized,
		"/api/metrics":   http.StatusUnauthorized,
		"/":              http.StatusOK,
		"/auth/login":    http.StatusFound,
		"/healthz":       http.StatusOK,
		"/no-such-thing": http.StatusOK,
	} {
		if w := serveAs(s, "", localRequest(http.MethodGet, path, nil)); w.Code != want {
			t.Errorf("anonymous GET %s answered %d: %s; want %d", path, w.Code, w.Body.String(), want)
		}
	}
	if w := serveAs(s, "nobody", localRequest(http.MethodGet, "/metrics", nil)); w.Code != http.StatusForbidden {
		t.Errorf("GET /metrics with no role answered %d", w.Code)
	}
	if w := serveAs(s, "viewer", localRequest(http.MethodGet, "/metrics", nil)); w.Code != http.StatusOK {
		t.Errorf("a viewer's GET /metrics answered %d: %s", w.Code, w.Body.String())
	}
}

// A load balancer probes by the target's address, which no host list names and which signs in as
// nobody: /healthz answers it with a constant and nothing else, and every other route still refuses.
func TestALoadBalancerCanProbeHealthAndNothingElse(t *testing.T) {
	s, _ := newServer(t)
	for path, want := range map[string]int{"/healthz": http.StatusOK, "/api/frontier": http.StatusForbidden} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "10.0.42.7:8080"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != want || (path == "/healthz" && w.Body.String() != "ok\n") {
			t.Errorf("GET %s by address = %d %q; want %d", path, w.Code, w.Body.String(), want)
		}
	}
}

// The probe port is what a cluster's kubelet and scraper reach by the pod's address, with no session
// and no host list to name them. It answers health and metrics, and nothing that reads or changes
// what ABOM holds, so binding the interface to loopback leaves nothing else reachable that way.
func TestTheProbePortServesHealthAndMetricsAndNothingElse(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.Access = signedIn
	s.Diagnostics = true
	probes := s.ProbeHandler()
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodGet, "/metrics", http.StatusOK},
		{http.MethodGet, "/api/summary", http.StatusNotFound},
		{http.MethodGet, "/api/me", http.StatusNotFound},
		{http.MethodGet, "/debug/pprof/", http.StatusNotFound},
		{http.MethodPost, "/api/discovery/start", http.StatusNotFound},
		{http.MethodGet, "/", http.StatusNotFound},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Host = "10.0.42.7:9090"
		w := httptest.NewRecorder()
		probes.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s on the probe port answered %d; want %d", c.method, c.path, w.Code, c.want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.Host = "10.0.42.7:9090"
	w := httptest.NewRecorder()
	probes.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "# TYPE abom_frontier_rows gauge") {
		t.Errorf("the probe port's metrics are not the coverage metrics: %.200s", w.Body.String())
	}
}

// Which codeql or copilot binary the coordinator runs is the coordinator's configuration, not an
// authoring choice: an analyst may start sessions, and only an administrator names the programs.
func TestOnlyAnAdministratorChoosesWhichToolBinaryRuns(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.Access = people{
		"analyst": {ID: "https://idp.example#s4", Username: "ana@example.com", Role: authz.RoleAnalyst},
		"admin":   {ID: "https://idp.example#s5", Username: "ada@example.com", Role: authz.RoleAdmin},
	}
	put := func() *http.Request {
		r := localRequest(http.MethodPut, "/api/authoring/tools/copilot", strings.NewReader(`{"path":""}`))
		r.Header.Set(controlHeader, "1")
		return r
	}
	if w := serveAs(s, "analyst", put()); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "admin") {
		t.Errorf("an analyst choosing the copilot binary answered %d: %s", w.Code, w.Body.String())
	}
	if w := serveAs(s, "admin", put()); w.Code == http.StatusForbidden {
		t.Errorf("an administrator choosing the copilot binary was refused: %s", w.Body.String())
	}
}
