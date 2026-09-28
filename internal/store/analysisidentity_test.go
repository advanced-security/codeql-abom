package store

import "testing"

// A row's age is evidence: a dependency analyzed a month ago and one analyzed this morning are
// different facts, and "re-analyze this tier" replaces an age distribution the reader cannot
// otherwise see. The column has been populated since the recheck loop landed and reached no
// caller, which is why nothing noticed it was missing from the view.
func TestTheQueueCarriesWhenEachDependencyWasAnalyzed(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "analyzed-yesterday", "done", 0)
	if _, err := s.DB().Exec(
		`UPDATE abom_frontier SET analyzed_at='2026-01-01T01:00:00Z'
		 WHERE node_key='analyzed-yesterday'`,
	); err != nil {
		t.Fatalf("recording an analysis time: %v", err)
	}

	items, err := s.FrontierSnapshot("done", 10)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one", items)
	}
	if items[0].AnalyzedAt != "2026-01-01T01:00:00Z" {
		t.Errorf("AnalyzedAt = %q, want the recorded time", items[0].AnalyzedAt)
	}
}

// A queued dependency has never been analyzed, so it has no time to report. Reporting one would
// be the same error as drawing an unmeasured node at the smallest size.
func TestAQueuedDependencyReportsNoAnalysisTime(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "never-analyzed", "pending", 0)

	items, err := s.FrontierSnapshot("pending", 10)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want one", items)
	}
	if items[0].AnalyzedAt != "" {
		t.Errorf("AnalyzedAt = %q, want empty for something never analyzed", items[0].AnalyzedAt)
	}
}

// The tier header sits above the control that binds a suite to a tier, so the number it needs is
// how many dependencies that tier holds, not only the analyzed ones.
func TestATierReportsItsSizeAndNotOnlyWhatIsAnalyzed(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "done-one", "done", 0)
	queueRow(t, s, "queued-one", "pending", 0)
	queueRow(t, s, "running-one", "running", 0)

	counts, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	if counts[TierDirect].Analyzed != 1 {
		t.Errorf("analyzed = %d, want the one done row", counts[TierDirect].Analyzed)
	}
	if counts[TierDirect].Total != 3 {
		t.Errorf("total = %d, want every row in the tier whatever its state",
			counts[TierDirect].Total)
	}
}

// Re-analyzing a tier moves its rows out of `done` and must not make the tier look empty.
func TestRequeueingATierLeavesItsSizeAlone(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "done-one", "done", 0)
	queueRow(t, s, "done-two", "done", 0)

	before, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	if _, err := s.RequeueTier(TierDirect); err != nil {
		t.Fatalf("RequeueTier: %v", err)
	}
	after, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}

	if after[TierDirect].Total != before[TierDirect].Total {
		t.Errorf("tier size went %d -> %d across a requeue; membership does not depend on state",
			before[TierDirect].Total, after[TierDirect].Total)
	}
	if after[TierDirect].Analyzed != 0 {
		t.Errorf("analyzed = %d after a requeue, want none", after[TierDirect].Analyzed)
	}
}

// A row has to name the tier it is in, not the raw hop count. Depth is 1 for every row at the top
// of the ranking — commonality and directness correlate — so the number reads as a constant
// exactly where anyone looks, while the fact that matters is whether the reader can fix this
// themselves or it arrives through an intermediary someone else owns.
func TestEachQueuedDependencyNamesItsTier(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "direct-one", "done", 0)
	if _, err := s.DB().Exec(
		`INSERT INTO abom_frontier (node_key, owner, repo, depth, status, attempts, score,
		    enqueued_at)
		 VALUES ('deep-one', 'octo', 'deep', 4, 'done', 0, 1, '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("inserting a transitive row: %v", err)
	}

	items, err := s.FrontierSnapshot("done", 10)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.NodeKey] = it.Tier
	}
	if got["direct-one"] != TierDirect {
		t.Errorf("direct-one is tier %q, want %q", got["direct-one"], TierDirect)
	}
	if got["deep-one"] != TierTransitive {
		t.Errorf("deep-one is tier %q, want %q", got["deep-one"], TierTransitive)
	}
}

// The row and the tier header are two renderings of one fact. If they can disagree, the grid says
// a suite runs on 458 dependencies while the rows beside it claim a different membership.
func TestARowsTierAgreesWithWhatTheTierCounted(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "one", "done", 0)
	queueRow(t, s, "two", "done", 0)

	items, err := s.FrontierSnapshot("", 100)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	perTier := map[string]int{}
	for _, it := range items {
		perTier[it.Tier]++
	}
	counts, err := s.TierCounts()
	if err != nil {
		t.Fatalf("TierCounts: %v", err)
	}
	for tier, n := range perTier {
		if counts[tier].Total != n {
			t.Errorf("rows put %d in %q, the header says %d", n, tier, counts[tier].Total)
		}
	}
}
