package main

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/expand"
)

// The round loop stops when a round moved nothing, which is how expand-all knows the queue is
// drained. A round that skipped every repository moved plenty — it settled all of them — so
// reading it as an empty queue abandons whatever is still queued behind it.
func TestARoundThatOnlySkippedIsNotMistakenForADrainedQueue(t *testing.T) {
	if !roundDidWork(expand.Stats{Skipped: 12, Items: 30}) {
		t.Error("a round that skipped 12 repositories reported no work, so the loop would stop")
	}
}

func TestADrainedQueueStopsTheRoundLoop(t *testing.T) {
	if roundDidWork(expand.Stats{}) {
		t.Error("an empty round reported work, so the loop would run to its cap")
	}
}

// Discoveries alone do not keep the loop going: they are enqueued by a round that also analyzed
// something, and counting them would spin on a queue whose next retry is hours away.
func TestTheRoundLoopKeepsGoingWhileAnythingIsBeingAttempted(t *testing.T) {
	for name, stats := range map[string]expand.Stats{
		"analyzed":    {Processed: 1},
		"skipped":     {Skipped: 1},
		"failed":      {Failed: 1},
		"unavailable": {Unavailable: 1},
	} {
		if !roundDidWork(stats) {
			t.Errorf("a round that %s reported no work", name)
		}
	}
}
