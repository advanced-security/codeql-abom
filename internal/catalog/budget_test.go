package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// A crawl that can read its budget can be told how much of it is left. Reading the headers only
// after a 403 means the first thing ABOM learns about its budget is that it has run out.
func TestTheBudgetIsReadFromAnAnswerThatSucceeded(t *testing.T) {
	reset := time.Now().Add(42 * time.Minute).Truncate(time.Second)
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-ratelimit-limit", "5000")
		w.Header().Set("x-ratelimit-remaining", "4987")
		w.Header().Set("x-ratelimit-reset", strconv.FormatInt(reset.Unix(), 10))
		fmt.Fprint(w, listBody)
	})

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}

	got := c.Stats().RateLimit
	if !got.Known {
		t.Fatal("the answer carried a budget, so it is known")
	}
	if got.Remaining != 4987 || got.Limit != 5000 {
		t.Errorf("want 4987 of 5000; got %d of %d", got.Remaining, got.Limit)
	}
	if !got.ResetAt.Equal(reset) {
		t.Errorf("want reset at %s; got %s", reset, got.ResetAt)
	}
}

// The invariant this exists for: a budget nobody reported is not a budget of nothing. Rendering
// "0 remaining" for an account whose headers never arrived would stop a crawl that had every right
// to run.
func TestAnUnreportedBudgetIsUnknownRatherThanZero(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, listBody)
	})

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}

	got := c.Stats().RateLimit
	if got.Known {
		t.Errorf("no header carried a budget, so none is known; got %+v", got)
	}
	if got.Remaining != 0 {
		t.Errorf("an unknown budget holds no number to report; got %d", got.Remaining)
	}
}

// The newest answer is the only one that describes the budget now.
func TestTheBudgetIsTheMostRecentOneReported(t *testing.T) {
	remaining := []string{"4999", "4998", "4997"}
	var n int
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-ratelimit-remaining", remaining[n])
		n++
		fmt.Fprint(w, listBody)
	})

	for range remaining {
		if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.Stats().RateLimit.Remaining; got != 4997 {
		t.Errorf("the latest answer is the current budget; got %d", got)
	}
}
