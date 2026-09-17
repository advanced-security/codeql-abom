package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/work"
)

func budgetServer(t *testing.T, remaining int, resetIn time.Duration) *Client {
	t.Helper()
	return testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if remaining >= 0 {
			w.Header().Set("x-ratelimit-limit", "5000")
			w.Header().Set("x-ratelimit-remaining", strconv.Itoa(remaining))
			w.Header().Set("x-ratelimit-reset",
				strconv.FormatInt(time.Now().Add(resetIn).Unix(), 10))
		}
		fmt.Fprint(w, listBody)
	})
}

// The invariant: a budget nobody reported is not a budget of nothing. Pacing on an unknown budget
// would throttle a crawl for a limit that was never stated.
func TestAnUnknownBudgetDoesNotPace(t *testing.T) {
	c := budgetServer(t, -1, 0)
	c.Pace = work.NewLimiter(0)

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	if got := c.Pace.Interval(); got != 0 {
		t.Errorf("no budget was reported, so nothing justifies a wait; got %s", got)
	}
}

// Few requests left and an hour before they come back is exactly when spreading them matters.
func TestATightBudgetSpreadsWhatIsLeft(t *testing.T) {
	c := budgetServer(t, 60, time.Hour)
	c.Pace = work.NewLimiter(0)

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}

	got := c.Pace.Interval()
	if got <= 0 {
		t.Fatal("60 requests over an hour has to pace")
	}
	// 60 left across an hour is one a minute, within the second it takes to run this.
	if got < 55*time.Second || got > 65*time.Second {
		t.Errorf("want roughly a minute between requests; got %s", got)
	}
}

// A budget with room must not be spread at all. Measured before this rule existed: 14,967 of
// 15,000 requests remaining still produced 240ms between reads, turning a 26-request crawl into
// 13 seconds of waiting for a budget nothing was threatening.
func TestAPlentifulBudgetDoesNotPaceAtAll(t *testing.T) {
	c := budgetServer(t, 14900, time.Hour)
	c.Pace = work.NewLimiter(0)

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	if got := c.Pace.Interval(); got != 0 {
		t.Errorf("14,900 of 5,000 reported left is not a reason to wait; got %s", got)
	}
}

// A budget that recovered has to lift the throttle it caused, or one tight window slows every
// window after it.
func TestARecoveredBudgetLiftsItsOwnThrottle(t *testing.T) {
	c := budgetServer(t, 4900, time.Hour)
	c.Pace = work.NewLimiter(60)

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	if got := c.Pace.Interval(); got != 0 {
		t.Errorf("the budget is healthy again, so nothing is held; got %s", got)
	}
}

// Nothing left reads as perHour=0, and zero means *unlimited* to the limiter — so computing the
// rate naively would make an exhausted budget the fastest setting there is. The 403 path already
// sleeps until the reset, and two mechanisms fighting over that is worse than one.
func TestAnExhaustedBudgetIsLeftToTheBackoff(t *testing.T) {
	c := budgetServer(t, 0, time.Hour)
	c.Pace = work.NewLimiter(120)
	before := c.Pace.Interval()

	if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
		t.Fatal(err)
	}
	if got := c.Pace.Interval(); got != before {
		t.Errorf("an exhausted budget must not relax the pace; %s became %s", before, got)
	}
}

// Waiting is reported so a crawl that was throttled can be told from one that was not.
func TestTheClientReportsWhatItWaited(t *testing.T) {
	// Under the threshold so the pace engages, and a near reset so the spacing is milliseconds.
	c := budgetServer(t, 300, 10*time.Second)
	c.Pace = work.NewLimiter(0)

	// Three: the first read has no pace yet, the second arms it, the third is the one held.
	for i := 0; i < 3; i++ {
		if _, err := c.ListDatabases(context.Background(), "octo-org/app"); err != nil {
			t.Fatal(err)
		}
	}

	got := c.Stats()
	if got.Waits < 1 || got.Waited <= 0 {
		t.Errorf("a paced crawl was held, and holding has to be reported; got %d over %s",
			got.Waits, got.Waited)
	}
}
