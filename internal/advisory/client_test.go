package advisory

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// recorder serves canned pages and records what was asked.
type recorder struct {
	mu       sync.Mutex
	requests []*http.Request
	// pages maps a query cursor to a response body; "" is the first page.
	pages map[string]string
	// nextOf maps a cursor to the cursor that follows it.
	nextOf map[string]string
	status int
	header map[string]string
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.requests = append(r.requests, req.Clone(context.Background()))
	r.mu.Unlock()

	for k, v := range r.header {
		w.Header().Set(k, v)
	}
	if r.status != 0 && r.status != http.StatusOK {
		w.WriteHeader(r.status)
		fmt.Fprint(w, `{"message":"nope"}`)
		return
	}

	cursor := req.URL.Query().Get("after")
	if next, ok := r.nextOf[cursor]; ok {
		w.Header().Set("Link",
			fmt.Sprintf(`<%s://%s/advisories?after=%s>; rel="next"`,
				"http", req.Host, next))
	}
	body, ok := r.pages[cursor]
	if !ok {
		body = "[]"
	}
	fmt.Fprint(w, body)
}

func newServer(t *testing.T, r *recorder) *Client {
	t.Helper()
	if r.pages == nil {
		r.pages = map[string]string{}
	}
	if r.nextOf == nil {
		r.nextOf = map[string]string{}
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, HTTP: srv.Client()}
}

func fixtureBody(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return string(b)
}

func (r *recorder) asked() []*http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*http.Request, len(r.requests))
	copy(out, r.requests)
	return out
}

// The whole package list travels in the query string, so it has to be split or a large
// organisation's list would overrun the URL.
func TestFetchBatchesPackages(t *testing.T) {
	rec := &recorder{pages: map[string]string{"": "[]"}}
	c := newServer(t, rec)
	c.BatchSize = 2

	if _, err := c.Fetch(context.Background(),
		[]string{"a/one", "b/two", "c/three", "d/four", "e/five"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	got := rec.asked()
	if len(got) != 3 {
		t.Fatalf("made %d requests, want 3 batches", len(got))
	}
	var affects []string
	for _, req := range got {
		affects = append(affects, req.URL.Query().Get("affects"))
	}
	want := []string{"a/one,b/two", "c/three,d/four", "e/five"}
	for i := range want {
		if affects[i] != want[i] {
			t.Errorf("batch %d affects = %q, want %q", i, affects[i], want[i])
		}
	}
}

// Stopping at the first page would silently under-report a heavily affected batch.
func TestFetchFollowsPagination(t *testing.T) {
	body := fixtureBody(t)
	rec := &recorder{
		pages:  map[string]string{"": body, "second": `[]`},
		nextOf: map[string]string{"": "second"},
	}
	c := newServer(t, rec)

	got, err := c.Fetch(context.Background(), []string{"github/codeql-action"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rec.asked()) != 2 {
		t.Errorf("made %d requests, want 2: the next link was not followed", len(rec.asked()))
	}
	if len(got) != 4 {
		t.Errorf("collected %d advisories, want 4", len(got))
	}
}

// The same advisory can affect packages in more than one batch.
func TestFetchDeduplicatesAcrossBatches(t *testing.T) {
	rec := &recorder{pages: map[string]string{"": fixtureBody(t)}}
	c := newServer(t, rec)
	c.BatchSize = 1

	got, err := c.Fetch(context.Background(), []string{"a/one", "b/two", "c/three"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(rec.asked()) != 3 {
		t.Fatalf("made %d requests, want 3", len(rec.asked()))
	}
	if len(got) != 4 {
		t.Errorf("collected %d advisories, want the 4 distinct ones", len(got))
	}
}

func TestFetchSendsTokenWhenSet(t *testing.T) {
	rec := &recorder{pages: map[string]string{"": "[]"}}
	c := newServer(t, rec)
	c.Token = "secret-value"

	if _, err := c.Fetch(context.Background(), []string{"a/one"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := rec.asked()[0].Header.Get("Authorization"); got != "Bearer secret-value" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestFetchOmitsAuthorizationWithoutToken(t *testing.T) {
	rec := &recorder{pages: map[string]string{"": "[]"}}
	c := newServer(t, rec)

	if _, err := c.Fetch(context.Background(), []string{"a/one"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := rec.asked()[0].Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want none", got)
	}
}

// Unauthenticated callers get 60 requests an hour, so the exhaustion message has to say what
// to do about it rather than surfacing a bare 403.
func TestFetchExplainsRateLimitExhaustion(t *testing.T) {
	rec := &recorder{
		status: http.StatusForbidden,
		header: map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1700000000"},
	}
	c := newServer(t, rec)

	_, err := c.Fetch(context.Background(), []string{"a/one"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error %q does not say how to raise the limit", err)
	}
}

func TestFetchSurfacesOtherFailures(t *testing.T) {
	rec := &recorder{status: http.StatusInternalServerError}
	c := newServer(t, rec)

	_, err := c.Fetch(context.Background(), []string{"a/one"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "rate limit") {
		t.Errorf("a server error was reported as a rate limit: %v", err)
	}
}

func TestFetchStopsOnCancellation(t *testing.T) {
	rec := &recorder{pages: map[string]string{"": "[]"}}
	c := newServer(t, rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Fetch(ctx, []string{"a/one"}); err == nil {
		t.Error("Fetch ignored a cancelled context")
	}
}

func TestBatchHandlesExactMultiples(t *testing.T) {
	got := Batch([]string{"a", "b", "c", "d"}, 2)
	if len(got) != 2 {
		t.Fatalf("made %d batches, want 2", len(got))
	}
	if len(got[1]) != 2 {
		t.Errorf("last batch = %v, want two entries", got[1])
	}
}

func TestBatchOfNothing(t *testing.T) {
	if got := Batch(nil, 10); len(got) != 0 {
		t.Errorf("Batch(nil) = %v, want none", got)
	}
}
