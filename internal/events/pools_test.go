package events

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/work"
)

// Following up an event is work of its own, so what its probes cost is on the event follow-up
// meter, beside polling and the sweep, and not hidden in theirs.
func TestTheEventProcessorsProbeSpendsFromTheEventFollowUpPool(t *testing.T) {
	f := newFixture(t)
	f.answers["acme/service@"] = shaB
	f.deliver("d-1", "push", pushPayload("acme/service", "refs/heads/main", shaA, shaB))
	pools := work.NewTokenPools()
	p := f.processor()
	p.Pace = pools.Pool(work.PoolEvents, work.UnitGit)

	if _, err := p.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	spent := map[string]work.Spend{}
	for _, s := range pools.Drain() {
		got := spent[s.Pool+"/"+s.Unit]
		got.Requests += s.Requests
		got.Charged += s.Charged
		spent[s.Pool+"/"+s.Unit] = got
	}
	if got := spent[work.PoolEvents+"/"+work.UnitGit]; got.Requests != 1 || got.Charged != 1 {
		t.Errorf("one probe is one git request the event follow-up pool paid for; got %+v", got)
	}
	if len(spent) != 1 {
		t.Errorf("an event's probe spends from its own pool and nobody else's; got %+v", spent)
	}
}
