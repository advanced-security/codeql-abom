package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func newServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return &Server{Store: s}, s
}

func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
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

// seedGraph gives the server something to answer with: one repository using one action.
func seedGraph(t *testing.T, s *store.Store) {
	t.Helper()
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := store.NewWriter(s, 0)
	for _, n := range []store.Node{
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repoID,
			Path: ".github/workflows/ci.yml"},
		{Key: "step:app:0", Kind: "step", RepositoryID: repoID,
			ContainerKey: "octo-org/app/.github/workflows/ci.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: "e1", SrcKey: "step:app:0", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefValue: "v4", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 10,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// What holds what is derived rather than drawn on demand, so a seeded corpus has to finish
	// the way an ingestion does or the canvas shows every node unattached.
	if _, err := s.DeriveContainment(); err != nil {
		t.Fatalf("DeriveContainment: %v", err)
	}
}

func TestSummaryEndpoint(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	got := decode[store.Summary](t, get(t, srv, "/api/summary"))
	if got.SeedRepos != 1 || got.Edges != 1 {
		t.Errorf("summary = %+v, want one repository and one edge", got)
	}
}

func TestGraphEndpointReturnsScopedNeighborhood(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	got := decode[store.Subgraph](t, get(t, srv, "/api/graph?focus=actions/checkout&hops=1"))
	if got.Focus != "actions/checkout" {
		t.Errorf("Focus = %q", got.Focus)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Errorf("got %d nodes / %d edges, want the action and its workflow",
			len(got.Nodes), len(got.Edges))
	}
}

// References are evidence and must reach the interface unaltered.
func TestGraphEndpointCarriesObservedReferences(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	got := decode[store.Subgraph](t, get(t, srv, "/api/graph?focus=actions/checkout&hops=1"))
	if len(got.Edges) == 0 || len(got.Edges[0].Refs) == 0 || got.Edges[0].Refs[0] != "v4" {
		t.Errorf("edges = %+v, want the observed reference", got.Edges)
	}
}

// A view with no focus has no meaning, and answering with the whole corpus would be worse than
// refusing.
func TestGraphEndpointRequiresAFocus(t *testing.T) {
	srv, _ := newServer(t)

	rec := get(t, srv, "/api/graph")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] == "" {
		t.Error("no error message returned")
	}
}

func TestGraphEndpointHonoursGranularity(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	repoID, _ := s.UpsertRepository("octo-org/app", "")
	w := store.NewWriter(s, 0)
	if err := w.AddNode(store.Node{
		Key: "repo:octo-org/app", Kind: "repo", RepositoryID: repoID}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got := decode[store.Subgraph](t, get(t,
		srv, "/api/graph?focus=actions/checkout&hops=1&granularity=repository"))
	for _, n := range got.Nodes {
		if n.Kind == "workflow" {
			t.Errorf("%s was not rolled up", n.Key)
		}
	}
}

func TestRemediationEndpoint(t *testing.T) {
	srv, _ := newServer(t)

	got := decode[store.AdvisoriesPage](t, get(t, srv, "/api/remediation?top=5"))
	if len(got.Items) != 0 {
		t.Errorf("empty database returned %d items", len(got.Items))
	}
	// The total is what the page was cut from, so it has to be reported even when it is zero.
	if got.Total != 0 {
		t.Errorf("empty database reported a match of %d", got.Total)
	}
}

func TestExposureEndpointSelectsGrouping(t *testing.T) {
	srv, _ := newServer(t)

	if rec := get(t, srv, "/api/exposure?by=advisory"); rec.Code != http.StatusOK {
		t.Errorf("by=advisory status %d", rec.Code)
	}
	if rec := get(t, srv, "/api/exposure?by=repository"); rec.Code != http.StatusOK {
		t.Errorf("by=repository status %d", rec.Code)
	}
}

func TestFrontierEndpoint(t *testing.T) {
	srv, s := newServer(t)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, score, enqueued_at)
		VALUES ('a/one','a','one',1,'pending',10,'2026-01-01'),
		       ('b/two','b','two',2,'done',5,'2026-01-01')`); err != nil {
		t.Fatalf("seeding frontier: %v", err)
	}

	all := decode[[]store.FrontierItem](t, get(t, srv, "/api/frontier"))
	if len(all) != 2 {
		t.Fatalf("returned %d items, want both states", len(all))
	}
	if all[0].NodeKey != "a/one" {
		t.Errorf("first item = %q, want the highest scoring", all[0].NodeKey)
	}

	pending := decode[[]store.FrontierItem](t, get(t, srv, "/api/frontier?status=pending"))
	if len(pending) != 1 {
		t.Errorf("filtered to %d items, want 1", len(pending))
	}
}

// The question the interface exists to answer about a dependency: not whether the name appears
// somewhere, but which repositories under review actually reach it and through what.
func TestNodeReachNamesWhoActuallyGetsThere(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	reach := decode[[]graph.NodeReach](t, get(t, srv, "/api/node/actions/checkout/-/reach"))
	if len(reach) != 1 {
		t.Fatalf("reach = %+v, want the one repository that uses it", reach)
	}
	if reach[0].Repository != "octo-org/app" || reach[0].Hops != 1 {
		t.Errorf("reach = %+v, want octo-org/app at one hop", reach[0])
	}
}

// Every observation names the query that made it, so a reader can always ask why a fact is
// there. Serving them unattributed would break the rule the whole store is built on.
func TestNodeObservationsCarryTheirQuery(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	run, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "abom/step-conditions", Repository: "octo-org/app",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if _, err := s.AddObservations(run, []store.Observation{{
		SubjectKind: store.SubjectNode, SubjectKey: "actions/checkout",
		Name: "condition", Value: "github.ref == 'refs/heads/main'",
	}}); err != nil {
		t.Fatalf("AddObservations: %v", err)
	}

	obs := decode[[]store.StoredObservation](t, get(t, srv, "/api/node/actions/checkout/-/observations"))
	if len(obs) != 1 {
		t.Fatalf("observations = %+v, want the one recorded", obs)
	}
	if obs[0].QueryID != "abom/step-conditions" {
		t.Errorf("QueryID = %q, want the query that produced it", obs[0].QueryID)
	}
	if obs[0].Value != "github.ref == 'refs/heads/main'" {
		t.Errorf("Value = %q, want the condition verbatim", obs[0].Value)
	}
}

// The interface is a single-page application, so a reload of a client-side route has to reach
// its entry point rather than a 404.
func TestUIFallsBackToTheEntryPoint(t *testing.T) {
	srv, _ := newServer(t)
	srv.UI = fstest.MapFS{
		"index.html":  &fstest.MapFile{Data: []byte("<html>abom</html>")},
		"css/app.css": &fstest.MapFile{Data: []byte("body{}")},
	}

	if body := get(t, srv, "/remediation").Body.String(); body != "<html>abom</html>" {
		t.Errorf("client route served %q, want the entry point", body)
	}
	if body := get(t, srv, "/css/app.css").Body.String(); body != "body{}" {
		t.Errorf("asset served %q", body)
	}
}

// A node key ends in something like `ci.yml`, so a client route is not distinguishable from a
// file by looking for a dot. Where the path lives is the only reliable signal.
func TestUIFallsBackForRoutesThatLookLikeFiles(t *testing.T) {
	srv, _ := newServer(t)
	srv.UI = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>abom</html>")}}

	route := "/node/octo-org/app/.github/workflows/ci.yml"
	if body := get(t, srv, route).Body.String(); body != "<html>abom</html>" {
		t.Errorf("%s served %q, want the entry point", route, body)
	}
}

// A missing asset must still fail as a missing asset. Serving HTML in its place makes the
// browser report a syntax error in whatever it thought it was loading.
func TestUIDoesNotMaskAMissingAsset(t *testing.T) {
	srv, _ := newServer(t)
	srv.UI = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>abom</html>")}}

	for _, missing := range []string{"/js/graph.js", "/_framework/blazor.js", "/css/app.css"} {
		if rec := get(t, srv, missing); rec.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", missing, rec.Code)
		}
	}
}

// The API must keep answering when no interface is embedded, which is how the server runs in
// development and in scripts.
func TestAPIWorksWithoutUIAssets(t *testing.T) {
	srv, _ := newServer(t)

	if rec := get(t, srv, "/api/summary"); rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if rec := get(t, srv, "/"); rec.Code == http.StatusOK {
		t.Error("served a page with no interface assets configured")
	}
}

// A mistyped endpoint must fail as an endpoint. Falling through to the interface's HTML makes a
// caller report a JSON parse error, which says nothing about the actual mistake.
func TestUnknownAPIPathIsNotAnsweredWithHTML(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	srv.UI = fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>interface</html>")},
	}

	rec := get(t, srv, "/api/no-such-endpoint")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d for an unrouted API path, want 404; body: %s",
			rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "<html>") {
		t.Error("an unrouted API path was answered with the interface")
	}
}

// Every response says what it is and says not to guess. The export carries corpus names and the
// caller's own query string, and a document a browser re-reads as markup runs on this origin.
func TestEveryResponseNamesItsTypeAndRefusesSniffing(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	srv.UI = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>abom</html>")}}

	for _, path := range []string{
		"/api/summary",
		"/api/graph/export?format=dot",
		"/api/no-such-endpoint",
		"/",
	} {
		header := get(t, srv, path).Result().Header
		if header.Get("Content-Type") == "" {
			t.Errorf("%s: no Content-Type, so net/http types the body by looking at it", path)
		}
		if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", path, got)
		}
	}
}

// A handler that declared nothing is the case net/http answers by sniffing, and it sniffs HTML.
// The last writer before the wire is the only place that knows the handler is finished deciding.
func TestAnUndeclaredBodyIsTypedRatherThanSniffed(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &recorder{ResponseWriter: inner, status: http.StatusOK, marshalNanos: -1}

	if _, err := rec.Write([]byte("<html>whatever a handler forgot to name</html>")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if ct := inner.Result().Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the body named rather than guessed", ct)
	}
}
