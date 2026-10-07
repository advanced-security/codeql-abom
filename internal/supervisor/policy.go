package supervisor

import (
	"fmt"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Default aggregate policy settings.
//
// The rebuild costs corpus size. Between rebuilds each cycle derives reach from what changed, so the
// rebuild is the oracle that corrects an incremental mistake, not what keeps reach current.
const (
	// DefaultAfterEdges is the materiality trigger. A rebuild that would change nothing is
	// pure cost.
	DefaultAfterEdges = 500
	// DefaultMaxAge bounds drift through a quiet period, whatever materiality says.
	DefaultMaxAge = 24 * time.Hour
	// DefaultMinInterval stops a burst of productive cycles becoming back-to-back rebuilds.
	DefaultMinInterval = 5 * time.Minute
)

// AggregatePolicy decides when reach is worth rebuilding wholesale.
type AggregatePolicy struct {
	AfterEdges  int
	MaxAge      time.Duration
	MinInterval time.Duration
}

func (p AggregatePolicy) afterEdges() int {
	if p.AfterEdges <= 0 {
		return DefaultAfterEdges
	}
	return p.AfterEdges
}

func (p AggregatePolicy) maxAge() time.Duration {
	if p.MaxAge <= 0 {
		return DefaultMaxAge
	}
	return p.MaxAge
}

func (p AggregatePolicy) minInterval() time.Duration {
	if p.MinInterval <= 0 {
		return DefaultMinInterval
	}
	return p.MinInterval
}

// due reports whether reach should be recomputed now, and why.
//
// The reason is returned rather than logged because it is shown to the operator, and different
// causes call for different next actions.
func (p AggregatePolicy) due(st store.ReachStaleness, drained bool, now time.Time) (bool, string) {
	if st.Never {
		return true, "reach has never been measured"
	}
	if st.Incomplete {
		return true, "reach was measured before this build counted what it shows"
	}

	age := now.Sub(st.At)
	if age < p.minInterval() {
		return false, ""
	}
	// A drain is when the numbers are read, but an idle crawl drains every cycle: only a drain after
	// something changed is worth what a rebuild costs.
	if drained && st.Generations > 0 {
		cycles := "cycles"
		if st.Generations == 1 {
			cycles = "cycle"
		}
		return true, fmt.Sprintf("the queue drained after %d %s changed the graph", st.Generations, cycles)
	}
	if drained && st.NewEdges > 0 {
		return true, fmt.Sprintf("the queue drained with %d dependencies arrived since", st.NewEdges)
	}
	if age >= p.maxAge() {
		return true, fmt.Sprintf("reach was last measured %s ago", age.Round(time.Minute))
	}

	threshold := p.afterEdges()
	// A percent of the corpus is the more honest bar once the corpus is large, so take
	// whichever asks for less.
	if onePercent := st.TotalEdges / 100; onePercent > 0 && onePercent < threshold {
		threshold = onePercent
	}
	if st.NewEdges >= threshold {
		return true, fmt.Sprintf("%d dependencies arrived since", st.NewEdges)
	}
	return false, ""
}
