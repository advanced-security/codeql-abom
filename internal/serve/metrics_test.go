package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A route nobody has called reports no samples rather than zero milliseconds.
//
// The same absent-is-not-zero rule the store follows. A latency of 0 ms for an endpoint that has
// never run reads as "instant" and would be quoted as one.
func TestAnUncalledRouteReportsNoSamplesRatherThanZero(t *testing.T) {
	m := newMetrics()

	got := m.snapshot()
	if len(got) != 0 {
		t.Fatalf("a server that has answered nothing reports %d routes, want none: %+v", len(got), got)
	}
}

// Aggregates key on the pattern the mux matched, never on the URL that arrived.
//
// `/api/repositories/{owner}/{name}` as a raw path grows the map with the corpus, which is how a
// metrics table becomes the leak it was added to find.
func TestMetricsAggregateByRoutePatternRatherThanURL(t *testing.T) {
	m := newMetrics()

	m.record("GET /api/repositories/{owner}/{name}", 200, 10, 100, 1)
	m.record("GET /api/repositories/{owner}/{name}", 200, 20, 200, 1)
	m.record("GET /api/summary", 200, 5, 50, 0)

	got := m.snapshot()
	if len(got) != 2 {
		t.Fatalf("two patterns produced %d buckets, want 2: %+v", len(got), got)
	}
	for _, r := range got {
		if r.Route == "GET /api/repositories/{owner}/{name}" && r.Count != 2 {
			t.Errorf("the templated route recorded %d samples, want 2", r.Count)
		}
	}
}

// A request the mux matched no pattern for is counted, and does not invent one.
//
// Without this, 404 traffic either vanishes from the totals or opens a bucket per bad URL, which
// is the unbounded key this is meant to avoid.
func TestAnUnmatchedRequestIsCountedUnderOneBucket(t *testing.T) {
	m := newMetrics()

	m.record("", 404, 1, 0, -1)
	m.record("", 404, 1, 0, -1)

	got := m.snapshot()
	if len(got) != 1 {
		t.Fatalf("unmatched requests produced %d buckets, want 1: %+v", len(got), got)
	}
	if got[0].Count != 2 {
		t.Errorf("recorded %d unmatched requests, want 2", got[0].Count)
	}
	if strings.Contains(got[0].Route, "/") {
		t.Errorf("the unmatched bucket named a path (%q); it must not key on the URL", got[0].Route)
	}
}

// Both diagnostic surfaces are absent unless the operator asked for them.
//
// Route names and latency distributions are a map of what exists and what is expensive, and
// pprof exposes goroutine stacks and heap contents. One flag rather than two, because two invites
// the answer "metrics are the safe half" and the safe half is the one that runs all the time.
func TestDiagnosticsAreAbsentWithoutTheFlag(t *testing.T) {
	srv, _ := newServer(t)

	for _, path := range []string{"/api/metrics", "/debug/pprof/"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d without --diagnostics, want 404", path, rec.Code)
		}
	}
}

// A permanent fallback to the full payload is invisible from the outside: the interface looks
// correct while doing the most expensive thing available, which is why one went unnoticed across
// a compaction. The counter is what makes it observable.
func TestTheDeltaReportsWhetherItWasHonoredOrReset(t *testing.T) {
	srv, s := newServer(t)
	srv.Diagnostics = true
	seedGraph(t, s)
	h := srv.Handler()

	generation, err := s.RecordGraphGeneration(store.GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	// A cursor the server never issued is refused, which is correct and must be counted.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/graph/delta?since=999", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/graph/delta answered %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// And one it did issue is honored. Both halves, so a counter that increments the wrong field
	// cannot pass by only ever being asked about the other one.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/graph/delta?since=%d", generation), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/graph/delta answered %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	var got struct {
		Delta struct {
			Honored int64 `json:"honored"`
			Reset   int64 `json:"reset"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding /api/metrics: %v", err)
	}
	if got.Delta.Reset != 1 {
		t.Errorf("reset count = %d, want 1; a silent fallback is the thing this counts",
			got.Delta.Reset)
	}
	if got.Delta.Honored != 1 {
		t.Errorf("honored count = %d, want 1", got.Delta.Honored)
	}
}

// And present when it is set, so the flag is proved to do something in both directions.
func TestDiagnosticsAnswerWhenTheFlagIsSet(t *testing.T) {
	srv, _ := newServer(t)
	srv.Diagnostics = true
	h := srv.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/summary", nil))

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/metrics answered %d with --diagnostics, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/api/summary") {
		t.Errorf("/api/metrics does not report the route just served:\n%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/debug/pprof/ answered %d with --diagnostics, want 200", rec.Code)
	}
}

// Serving the metrics must not be the thing the metrics report on.
//
// Recording is on the request path, so an aggregate that reallocates or copies per request would
// show up as the cost of whatever it was installed to measure.
func TestRecordingDoesNotGrowWithRequestCount(t *testing.T) {
	m := newMetrics()

	for i := 0; i < 10_000; i++ {
		m.record("GET /api/summary", 200, int64(i%50), 100, 0)
	}

	got := m.snapshot()
	if len(got) != 1 {
		t.Fatalf("10,000 requests to one route produced %d buckets, want 1", len(got))
	}
	if got[0].Count != 10_000 {
		t.Errorf("recorded %d of 10,000 requests", got[0].Count)
	}
	if got[0].MaxMillis != 49 {
		t.Errorf("max is %d ms, want 49", got[0].MaxMillis)
	}
}

// Marshaling is the request phase this server can measure separately and report per route.
func TestMarshalingIsReportedApartFromTheRequest(t *testing.T) {
	m := newMetrics()

	m.record("GET /api/graph/full", 200, 100, 1_000_000, 5)

	got := m.snapshot()
	if len(got) != 1 {
		t.Fatalf("got %d routes, want 1", len(got))
	}
	if got[0].TotalMillis != 100 {
		t.Errorf("total is %d ms, want 100", got[0].TotalMillis)
	}
	if got[0].MarshalMillis != 5 {
		t.Errorf("marshal is %d ms, want 5", got[0].MarshalMillis)
	}
}

// A handler that never marshaled reports no marshal time, not zero.
//
// Absent is not zero here as everywhere else: pprof, the UI assets and a 404 all answer without
// encoding anything, and reporting 0 ms for them would read as "encoding was instant".
func TestARouteThatMarshalsNothingReportsNoMarshalTime(t *testing.T) {
	m := newMetrics()

	m.record("GET /debug/pprof/", 200, 12, 4096, -1)

	got := m.snapshot()
	if got[0].MarshalKnown {
		t.Errorf("a route that marshaled nothing claims a marshal time of %d ms",
			got[0].MarshalMillis)
	}
}
