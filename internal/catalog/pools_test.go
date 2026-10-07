package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/work"
)

// pooled wires a client to the enumeration pools, as serve does.
func pooled(c *Client) *work.TokenPools {
	pools := work.NewTokenPools()
	c.REST = pools.Pool(work.PoolEnumeration, work.UnitREST)
	c.GraphQL = pools.Pool(work.PoolEnumeration, work.UnitGraphQL)
	return pools
}

func spentBy(pools *work.TokenPools, unit string) (requests, charged int) {
	for _, s := range pools.Drain() {
		if s.Pool == work.PoolEnumeration && s.Unit == unit {
			requests += s.Requests
			charged += s.Charged
		}
	}
	return requests, charged
}

// GraphQL's `remaining` moves with everybody's traffic on the token, so it is no instrument: the
// pool records the cost each answer reports, and the gap between two readings of what is left is
// somebody else's spend as much as ours.
func TestGraphQLSpendIsTheCostChargedNotADifferenceOfWhatIsLeft(t *testing.T) {
	remaining := []int{4990, 4800}
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		left := remaining[n.Add(1)-1]
		fmt.Fprintf(w, `{"data":{"rateLimit":{"cost":3,"remaining":%d,"limit":5000,
		                 "resetAt":"2026-10-04T13:00:00Z"},"viewer":{"login":"octocat"}}}`, left)
	}))
	defer srv.Close()
	c := New(srv.URL, "t", srv.Client())
	pools := pooled(c)

	for range remaining {
		if err := c.graphQL(context.Background(), "query{viewer{login}}", nil, nil); err != nil {
			t.Fatal(err)
		}
	}

	requests, charged := spentBy(pools, work.UnitGraphQL)
	if requests != 2 || charged != 6 {
		t.Errorf("two queries at a cost of 3 each; got %d requests charged %d points", requests, charged)
	}
	got := c.GraphQL.Reading()
	if !got.Known || got.Remaining != 4800 || got.ResetAt.IsZero() {
		t.Errorf("the pool keeps what the last answer reported, reset included; got %+v", got)
	}
}

// A conditional read GitHub answers unchanged costs a round trip and no primary quota, so it is a
// request the pool counts and a charge it does not.
func TestAReadAnsweredUnchangedIsARequestAndNoCharge(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, listBody)
	})
	c.Cache = newFakeCache()
	pools := pooled(c)

	for range 2 {
		if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
			t.Fatal(err)
		}
	}

	if requests, charged := spentBy(pools, work.UnitREST); requests != 2 || charged != 1 {
		t.Errorf("two reads, one charged; got %d requests charged %d", requests, charged)
	}
}

// An exhausted allowance must not read as unlimited: the read after the one that spent the last
// request waits for the reset instead of being sent.
func TestAReadAfterTheAllowanceRanOutWaitsForTheReset(t *testing.T) {
	var asked atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.Header().Set("x-ratelimit-limit", "5000")
		w.Header().Set("x-ratelimit-remaining", "0")
		w.Header().Set("x-ratelimit-reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		fmt.Fprint(w, listBody)
	})
	pooled(c)

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.ListDatabases(ctx, "octo-org/app")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the reset is an hour away, so the read is still waiting for it; got %v", err)
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("nothing is sent while nothing is left; GitHub was asked %d times", got)
	}
}

// A read that would rather fall back than wait, the tree listing a clone can replace, is refused
// at once while the allowance is spent rather than held for up to an hour.
func TestAClientThatNeverRetriesIsRefusedWhileTheAllowanceIsSpent(t *testing.T) {
	var asked atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.Header().Set("x-ratelimit-remaining", "0")
		w.Header().Set("x-ratelimit-reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		fmt.Fprint(w, listBody)
	})
	c.MaxRetries = 0
	pooled(c)

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.ListDatabases(ctx, "octo-org/app")

	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a client that never retries is refused at once; got %v", err)
	}
	if got := asked.Load(); got != 1 {
		t.Errorf("a refused read is not sent; GitHub was asked %d times", got)
	}
}

// An answer carrying a reset time and no remaining count reported no allowance, and an allowance
// nobody reported must not hold the next read.
func TestAnAnswerThatReportsNoRemainingHoldsNothing(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-ratelimit-reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		fmt.Fprint(w, listBody)
	})
	pooled(c)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 3 {
		if _, err := c.ListDatabases(ctx, "octo-org/app"); err != nil {
			t.Fatalf("an unreported allowance held the read: %v", err)
		}
	}
	if got := c.REST.Reading(); got.Known {
		t.Errorf("no remaining count was reported, so none is known; got %+v", got)
	}
}

// The pool reads REST's allowance and GraphQL's apart: one answer must not overwrite the other.
func TestRESTAndGraphQLAnswersFeedTheirOwnPools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("x-ratelimit-resource", "graphql")
			w.Header().Set("x-ratelimit-remaining", "4999")
			fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":4999,"limit":5000}}}`)
			return
		}
		w.Header().Set("x-ratelimit-resource", "core")
		w.Header().Set("x-ratelimit-remaining", "4321")
		fmt.Fprint(w, listBody)
	}))
	defer srv.Close()
	c := New(srv.URL, "t", srv.Client())
	pooled(c)

	if err := c.graphQL(context.Background(), "query{rateLimit{cost}}", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	if got := c.GraphQL.Reading().Remaining; got != 4999 {
		t.Errorf("GraphQL's pool reads GraphQL's allowance; got %d", got)
	}
	if got := c.REST.Reading().Remaining; got != 4321 {
		t.Errorf("REST's pool reads REST's allowance; got %d", got)
	}
}
