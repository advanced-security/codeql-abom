package work

import (
	"context"
	"slices"
	"testing"
	"time"
)

// clocked builds a pool on a fake clock, so a test reads what it asked to sleep for.
func clocked(pool, unit string) (*TokenPool, *fakeClock) {
	c := newClock()
	pools := NewTokenPools()
	p := pools.Pool(pool, unit)
	p.limiter.now, p.limiter.sleep = c.Now, c.Sleep
	if p.ceiling != nil {
		p.ceiling.now, p.ceiling.sleep = c.Now, c.Sleep
	}
	p.now = c.Now
	return p, c
}

// The operator's rate is the only governor a pool has, and a burst is room taken at once.
func TestAConfiguredPoolLetsItsBurstThroughAndHoldsTheNext(t *testing.T) {
	p, c := clocked(PoolProbing, UnitGit)
	p.Configure(3600, 2)

	for range 3 {
		if err := p.Take(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.waits(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("a burst of two at one a second lets two through and holds the third a second; got %v", got)
	}
}

// A pool with room leaves the work alone: with no rate set, nothing is held however fast it asks.
func TestAPoolWithNoRateHoldsNobody(t *testing.T) {
	p, c := clocked(PoolFetching, UnitGit)

	for range 50 {
		if err := p.Take(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.waits(); len(got) != 0 {
		t.Errorf("no rate is set, so nothing justifies a wait; got %v", got)
	}
}

// An unknown budget is not a budget of nothing: an allowance nobody reported must not throttle,
// even where an answer carried a reset time and no remaining count.
func TestAnUnknownLimitDoesNotThrottle(t *testing.T) {
	p, c := clocked(PoolEnumeration, UnitREST)
	p.Observe(Reading{Known: false, ResetAt: c.Now().Add(time.Hour)})

	for range 50 {
		if err := p.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.waits(); len(got) != 0 {
		t.Errorf("no allowance was reported, so none can be exhausted; got %v", got)
	}
	if got := p.Reading(); got.Known || got.Remaining != 0 {
		t.Errorf("an unreported allowance has no figure; got %+v", got)
	}
}

// An exhausted allowance must not read as unlimited: the next request waits for the reset.
func TestAnExhaustedReportedRemainingWaitsUntilTheReset(t *testing.T) {
	p, c := clocked(PoolEnumeration, UnitREST)
	p.Observe(Reading{Known: true, Remaining: 0, Limit: 5000, ResetAt: c.Now().Add(10 * time.Minute)})

	if err := p.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.waits(); !slices.Equal(got, []time.Duration{10 * time.Minute}) {
		t.Errorf("nothing is left until the reset, so the request waits for it; got %v", got)
	}
}

// GitHub's allowance belongs to the token, so every pool that spends it sees one figure.
func TestPoolsOnOneProviderShareItsReportedAllowance(t *testing.T) {
	pools := NewTokenPools()
	reset := time.Now().Add(time.Hour)
	pools.Pool(PoolAdvisories, UnitREST).Observe(Reading{Known: true, Remaining: 0, ResetAt: reset})

	if _, spent := pools.Pool(PoolEnumeration, UnitREST).Exhausted(); !spent {
		t.Error("an allowance one pool saw run out is out for every pool spending it")
	}
	if _, spent := pools.Pool(PoolEnumeration, UnitGraphQL).Exhausted(); spent {
		t.Error("GraphQL points are a separate allowance from REST requests")
	}
}

// What one request was charged beyond the unit its wait reserved is owed by whoever asks next.
func TestAChargeAboveOneUnitIsOwedByTheNextRequest(t *testing.T) {
	p, c := clocked(PoolEnumeration, UnitGraphQL)
	p.Configure(3600, 1)

	if err := p.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.Spend(1, 5)
	if err := p.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.waits(); !slices.Equal(got, []time.Duration{5 * time.Second}) {
		t.Errorf("five points at one a second hold the next request five seconds; got %v", got)
	}
}

// Spend is recorded per pool, per unit and per minute, and a pool that spent nothing has no row.
func TestSpendIsDrainedByMinuteAndAbsentUntilSomethingIsSpent(t *testing.T) {
	pools := NewTokenPools()
	if got := pools.Drain(); len(got) != 0 {
		t.Fatalf("nothing was spent, so nothing is recorded; got %+v", got)
	}

	at := time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC)
	p := pools.Pool(PoolEnumeration, UnitREST)
	p.now = func() time.Time { return at }
	p.Spend(1, 1)
	p.Spend(1, 0)
	p.Observe(Reading{Known: true, Remaining: 4998, Limit: 5000, ResetAt: at.Add(time.Hour)})

	got := pools.Drain()
	if len(got) != 1 {
		t.Fatalf("one pool spent in one minute; got %+v", got)
	}
	row := got[0]
	if row.Pool != PoolEnumeration || row.Unit != UnitREST || !row.Bucket.Equal(at.Truncate(time.Minute)) {
		t.Errorf("want enumeration REST requests at 12:00; got %+v", row)
	}
	if row.Requests != 2 || row.Charged != 1 {
		t.Errorf("two requests, one charged; got %d and %d", row.Requests, row.Charged)
	}
	if !row.Reading.Known || row.Reading.Remaining != 4998 {
		t.Errorf("the minute keeps what GitHub last reported in it; got %+v", row.Reading)
	}
	if again := pools.Drain(); len(again) != 0 {
		t.Errorf("drained spend is not recorded twice; got %+v", again)
	}
}

// A pool nothing in this build draws on says so, so an empty meter is not read as idle work.
func TestEveryPoolNamesItsUnitAndAnUnmeteredOneSaysWhy(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range PoolCatalog {
		key := k.Pool + "/" + k.Unit
		if seen[key] {
			t.Errorf("%s is listed twice", key)
		}
		seen[key] = true
		if k.Unit == "" || k.Work == "" {
			t.Errorf("%s needs a unit and a description of its work", key)
		}
		if NewTokenPools().Pool(k.Pool, k.Unit) == nil {
			t.Errorf("%s is listed but no pool is built for it", key)
		}
	}
	var found bool
	for _, k := range PoolCatalog {
		if k.Pool == PoolCopilot {
			found = true
			if k.Unmetered == "" {
				t.Errorf("%s spends nothing in this build and has to say so", PoolCopilot)
			}
		}
	}
	if !found {
		t.Errorf("%s is a kind of work the plan names and is missing", PoolCopilot)
	}
}

// A unit the checker shared with discovery would let a busy checker starve the crawl, and make the
// freshness it measures look worse than it is.
func TestTheCheckerHasAMeteredPoolOfItsOwnInEveryUnitItSpends(t *testing.T) {
	want := map[string]string{UnitWorkerSeconds: "", UnitREST: ResourceCore, UnitGraphQL: ResourceGraphQL}
	for _, k := range PoolCatalog {
		if k.Pool != PoolConsistency {
			continue
		}
		resource, ok := want[k.Unit]
		if !ok {
			t.Errorf("the checker has a pool in %s, which nothing it does spends", k.Unit)
			continue
		}
		delete(want, k.Unit)
		if k.Unmetered != "" {
			t.Errorf("the checker's %s pool says %q, and the checker draws on it", k.Unit, k.Unmetered)
		}
		if k.Resource != resource {
			t.Errorf("the checker's %s pool spends the %q allowance, want %q", k.Unit, k.Resource, resource)
		}
	}
	for unit := range want {
		t.Errorf("the checker spends %s and has no pool in it", unit)
	}
}

// Analysis capacity is denominated in worker-seconds, so each preparation spends the time it held a
// worker for.
func TestEachPreparationSpendsItsWorkerSecondsFromTheAnalysisPool(t *testing.T) {
	pools := NewTokenPools()
	c := newClock()
	pool := &Pool{
		Concurrency: 1,
		Capacity:    pools.Pool(PoolAnalysis, UnitWorkerSeconds),
		now:         c.Now,
		Prepare: func(ctx context.Context, job Job) (Prepared, error) {
			return Prepared{Job: job}, c.Sleep(ctx, 90*time.Second)
		},
		Consume: func(context.Context, Prepared) (int, error) { return 0, nil },
	}

	if _, err := pool.Run(context.Background(), []Job{{Repository: "a/one"}, {Repository: "b/two"}}); err != nil {
		t.Fatal(err)
	}

	var requests, charged int
	for _, s := range pools.Drain() {
		requests += s.Requests
		charged += s.Charged
	}
	if requests != 2 || charged != 180 {
		t.Errorf("two preparations of 90 seconds each; got %d charged %d worker-seconds", requests, charged)
	}
}
