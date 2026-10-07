package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestReachIsRebuiltWhenItHasNeverBeenMeasured(t *testing.T) {
	due, why := AggregatePolicy{}.due(store.ReachStaleness{Never: true}, false, time.Now())
	if !due {
		t.Errorf("declined to measure reach that has never been measured (%q)", why)
	}
}

// A rebuild that would change nothing is pure cost, and its cost tracks corpus size rather than
// how much arrived.
func TestReachIsNotRebuiltForATrivialChange(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Hour), NewEdges: 4, TotalEdges: 15000}

	if due, why := (AggregatePolicy{}).due(st, false, now); due {
		t.Errorf("rebuilt reach for 4 new edges out of 15000: %q", why)
	}
}

func TestReachIsRebuiltOnceEnoughDependenciesArrive(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Hour), NewEdges: 600, TotalEdges: 15000}

	due, why := AggregatePolicy{}.due(st, false, now)
	if !due {
		t.Error("declined to rebuild reach after 600 new dependencies")
	}
	if why == "" {
		t.Error("gave no reason, which is what the operator is shown")
	}
}

// A fixed count is the wrong bar for a corpus of a few hundred edges: a change worth noticing
// there is far smaller than one worth noticing across a million.
func TestASmallCorpusLowersTheBarForRebuilding(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Hour), NewEdges: 12, TotalEdges: 900}

	if due, _ := (AggregatePolicy{}).due(st, false, now); !due {
		t.Error("held a 900-edge corpus to the same fixed threshold as a large one")
	}
}

// The same twelve edges say nothing about a corpus of a million.
func TestALargeCorpusIsNotRebuiltForTheSameHandfulOfEdges(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Hour), NewEdges: 12, TotalEdges: 1_000_000}

	if due, why := (AggregatePolicy{}).due(st, false, now); due {
		t.Errorf("rebuilt a million-edge corpus for 12 new edges: %q", why)
	}
}

// Nothing else will trigger during a quiet period, and a number nobody has checked in a day
// should not be presented as current.
func TestReachIsRebuiltOnceItPassesTheAgeCeiling(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-25 * time.Hour), NewEdges: 1, TotalEdges: 15000}

	if due, _ := (AggregatePolicy{}).due(st, false, now); !due {
		t.Error("left reach unmeasured for over a day")
	}
}

// Draining the queue is when the numbers are most likely to be read.
func TestQueueDrainingForcesARebuild(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Hour), NewEdges: 1, TotalEdges: 15000}

	if due, _ := (AggregatePolicy{}).due(st, true, now); !due {
		t.Error("finished a run without measuring what it had just built")
	}
}

// An idle crawl drains every cycle. Rebuilding each time the floor passed was 59 of 65 rebuilds with
// nothing new on the crawled organization, 21 s each on the store's one connection.
func TestADrainedQueueIsNotRebuiltWhenNothingChangedSinceTheLastRebuild(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Hour), TotalEdges: 15000}

	if due, why := (AggregatePolicy{}).due(st, true, now); due {
		t.Errorf("rebuilt reach on a drain with nothing changed since the last rebuild: %q", why)
	}
}

// A cycle can change the graph without adding an edge: a finding, a commit that moved, a prune.
func TestADrainedQueueIsRebuiltWhenCyclesChangedTheGraphAndSaysHowMany(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Hour), Generations: 3, TotalEdges: 15000}

	due, why := AggregatePolicy{}.due(st, true, now)
	if !due || !strings.Contains(why, "3") {
		t.Errorf("due %v because %q; want a rebuild naming the 3 cycles that changed the graph", due, why)
	}
}

// A burst of productive cycles must not turn into back-to-back full recomputes.
func TestTheFloorHoldsEvenWhenTheQueueDrains(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Second), NewEdges: 9000, TotalEdges: 15000}

	if due, why := (AggregatePolicy{}).due(st, true, now); due {
		t.Errorf("rebuilt reach a second after the last rebuild: %q", why)
	}
}

// Reach derived before this build counted what it now shows is missing those figures, and only a
// rebuild fills them; waiting for the minimum interval would leave them blank to no purpose.
func TestReachDerivedBeforeItsFiguresExistedIsRebuiltAtOnce(t *testing.T) {
	now := time.Now()
	st := store.ReachStaleness{At: now.Add(-time.Minute), Incomplete: true, TotalEdges: 15000}

	due, why := AggregatePolicy{}.due(st, false, now)
	if !due || why == "" {
		t.Errorf("declined to fill figures reach never counted (%v, %q)", due, why)
	}
}
