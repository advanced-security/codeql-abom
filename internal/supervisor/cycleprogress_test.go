package supervisor

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
)

// A cycle logs only when something happened, so a loop waiting on a retry hours away does not
// write two lines a minute all night. A re-analysis that finds everything already on record does
// happen: it settles every repository it claimed. Staying silent through that reads as a hung
// crawl, which is the one thing the log exists to rule out.
func TestACycleThatOnlySkippedIsStillReported(t *testing.T) {
	if !cycleProgressed(expand.Stats{Skipped: 40, Items: 96}) {
		t.Error("a cycle that skipped 40 repositories was treated as nothing happening")
	}
}

func TestAnIdleCycleStaysQuiet(t *testing.T) {
	if cycleProgressed(expand.Stats{}) {
		t.Error("an idle cycle would write a log line every time round the loop")
	}
}

// Skipping produced no new graph data, so the work that follows ingestion has nothing to chew on.
// Running it anyway would spend a full findings-to-node relink per cycle of a re-analysis.
func TestSkippingAloneIsNotTreatedAsIngestion(t *testing.T) {
	if cycleIngested(expand.Stats{Skipped: 40, Items: 96}) {
		t.Error("a skip-only cycle claimed to have ingested something")
	}
	if !cycleIngested(expand.Stats{Processed: 1, Edges: 12}) {
		t.Error("a cycle that analyzed a repository claimed to have ingested nothing")
	}
}

// The log has always said "skipped N already on record"; the status had nowhere to put it, so a
// re-analysis of work already on record moved no number the interface shows. Processed excludes
// skips by construction, which is why Analyzed cannot stand in for this.
func TestACycleThatOnlySkippedIsVisibleInTheStatus(t *testing.T) {
	var st Status

	recordCycle(&st, expand.Stats{Skipped: 40, Items: 96})

	if st.Skipped != 40 {
		t.Errorf("Skipped = %d, want the 40 the cycle log reports", st.Skipped)
	}
	if st.Analyzed != 0 {
		t.Errorf("Analyzed = %d, want 0: nothing was analyzed", st.Analyzed)
	}
	if st.Settled != 96 {
		t.Errorf("Settled = %d, want 96", st.Settled)
	}
}

// The checkout cache is wired into the loop, so serve is where reuse mostly happens. Accumulating
// it only in expand-all left the long-lived entrypoint with no way to say a cycle cloned nothing,
// which reads exactly like a cycle that cloned the whole corpus again.
func TestCheckoutReuseReachesTheStatus(t *testing.T) {
	var st Status

	recordCycle(&st, expand.Stats{Processed: 12, Reused: 9, Fetched: 3, Items: 12})

	if st.Reused != 9 {
		t.Errorf("Reused = %d, want the 9 trees the cycle did not clone", st.Reused)
	}
	if st.Analyzed != 12 {
		t.Errorf("Analyzed = %d, want 12: reuse is not a skip", st.Analyzed)
	}
	if st.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0: a reused tree was still analyzed", st.Skipped)
	}
}

// A starting point read with nothing to analyze is neither a failure nor unavailable, so the status
// and the cycle log count it apart: folded into unavailable it read as access to fix.
func TestNothingToAnalyzeIsCountedApartFromUnavailable(t *testing.T) {
	var st Status

	recordCycle(&st, expand.Stats{Items: 3, Unavailable: 1, Nothing: 2})

	if st.Nothing != 2 || st.Unavailable != 1 {
		t.Errorf("nothing = %d, unavailable = %d, want 2 and 1", st.Nothing, st.Unavailable)
	}
	if line := cycleLine(4, expand.Stats{Items: 3, Unavailable: 1, Nothing: 2}); !strings.Contains(line, "2 with nothing to analyze") {
		t.Errorf("cycle line %q does not say what had nothing to analyze", line)
	}
}
