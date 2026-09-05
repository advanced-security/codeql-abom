package supervisor

import (
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
