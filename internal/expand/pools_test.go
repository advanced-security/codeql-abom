package expand

import (
	"context"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/work"
)

func spentIn(pools *work.TokenPools) map[string]work.Spend {
	out := map[string]work.Spend{}
	for _, s := range pools.Drain() {
		got := out[s.Pool+"/"+s.Unit]
		got.Requests += s.Requests
		got.Charged += s.Charged
		out[s.Pool+"/"+s.Unit] = got
	}
	return out
}

// The sweep is a pool like any other, so what it costs is on a meter rather than inside the crawl's.
func TestTheRecheckSweepSpendsFromTheProbingPool(t *testing.T) {
	r, q, p, _ := newRechecker(t)
	analyzed(t, q, "actions", "checkout", frontier.HeadRef, "sha-1")
	analyzed(t, q, "actions", "cache", frontier.HeadRef, "sha-2")
	p.answers["actions/checkout@HEAD"] = "sha-1"
	p.answers["actions/cache@HEAD"] = "sha-3"
	stale(q, 48*time.Hour)
	pools := work.NewTokenPools()
	r.Pace = pools.Pool(work.PoolProbing, work.UnitGit)

	stats, err := r.Run(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}

	spent := spentIn(pools)
	if got := spent[work.PoolProbing+"/"+work.UnitGit]; got.Requests != stats.Checked || got.Charged != 2 {
		t.Errorf("each of the %d probes is a git request the probing pool paid for; got %+v", stats.Checked, got)
	}
	if len(spent) != 1 {
		t.Errorf("the sweep spends from its own pool and nobody else's; got %+v", spent)
	}
}

// A clone is a git request the fetching pool pays for, and each preparation spends worker time.
func TestAnExpansionSpendsFromTheFetchingAndAnalysisPools(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("a", "one", "", 1), item("b", "two", "", 1))
	pools := work.NewTokenPools()
	h.expander.Pace = pools.Pool(work.PoolFetching, work.UnitGit)
	h.expander.Capacity = pools.Pool(work.PoolAnalysis, work.UnitWorkerSeconds)

	h.run(10)

	spent := spentIn(pools)
	if got := spent[work.PoolFetching+"/"+work.UnitGit]; got.Requests != len(h.fetched) || len(h.fetched) != 2 {
		t.Errorf("each of the %d clones is a git request; got %+v", len(h.fetched), got)
	}
	if got := spent[work.PoolAnalysis+"/"+work.UnitWorkerSeconds]; got.Requests != 2 {
		t.Errorf("each repository prepared is one draw on analysis capacity; got %+v", got)
	}
}
