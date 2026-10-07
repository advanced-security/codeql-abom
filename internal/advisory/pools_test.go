package advisory

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/work"
)

// Advisory reads draw on a pool of their own, so their cost is not hidden inside the crawl's, and
// the pool keeps what GitHub reported left on the token they spend.
func TestAdvisoryReadsAreChargedToTheirPoolWithWhatGitHubReported(t *testing.T) {
	r := &recorder{
		pages:  map[string]string{"": "[]", "p2": "[]"},
		nextOf: map[string]string{"": "p2"},
		header: map[string]string{
			"X-RateLimit-Remaining": "4321", "X-RateLimit-Limit": "5000",
			"X-RateLimit-Reset": strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
		},
	}
	c := newServer(t, r)
	pools := work.NewTokenPools()
	c.Pace = pools.Pool(work.PoolAdvisories, work.UnitREST)

	if _, err := c.Fetch(context.Background(), []string{"actions/checkout"}); err != nil {
		t.Fatal(err)
	}

	var requests, charged int
	for _, s := range pools.Drain() {
		if s.Pool == work.PoolAdvisories {
			requests += s.Requests
			charged += s.Charged
		}
	}
	if requests != 2 || charged != 2 {
		t.Errorf("two pages, each a request GitHub charged; got %d requests charged %d", requests, charged)
	}
	if got := c.Pace.Reading(); !got.Known || got.Remaining != 4321 {
		t.Errorf("the pool keeps what GitHub reported; got %+v", got)
	}
}

// An exhausted allowance must not read as unlimited: the next page waits for the reset.
func TestAnAdvisoryPageAfterTheAllowanceRanOutWaitsForTheReset(t *testing.T) {
	r := &recorder{
		pages:  map[string]string{"": "[]", "p2": "[]"},
		nextOf: map[string]string{"": "p2"},
		header: map[string]string{
			"X-RateLimit-Remaining": "0",
			"X-RateLimit-Reset":     strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
		},
	}
	c := newServer(t, r)
	c.Pace = work.NewTokenPools().Pool(work.PoolAdvisories, work.UnitREST)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.Fetch(ctx, []string{"actions/checkout"})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the reset is an hour away, so the second page is still waiting; got %v", err)
	}
	if got := len(r.asked()); got != 1 {
		t.Errorf("nothing is sent while nothing is left; GitHub was asked %d times", got)
	}
}
