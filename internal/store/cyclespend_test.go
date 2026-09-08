package store

import (
	"testing"
)

// "Under budget" is a phrase until a cycle records what it spent. Nothing in the loop held a
// clock except the aggregate rebuild, so no cycle could say what it cost or what that bought.
func TestACycleRecordsWhatItSpentAndWhatItBought(t *testing.T) {
	s := newTestStore(t)

	if err := s.RecordCycle(CycleSpend{
		Cycle: 1, StartedAt: "2026-09-07T00:00:00Z", Millis: 15300,
		Fetches: 3, Builds: 3, Evaluations: 6, Probes: 12,
		Analyzed: 3, Skipped: 1, Settled: 2, Discovered: 40, Drifted: 1,
		ShallowestIncomplete: 2, HasOutstanding: true,
	}); err != nil {
		t.Fatalf("RecordCycle: %v", err)
	}

	got, err := s.RecentCycles(10)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d cycles, want 1", len(got))
	}
	if got[0].Millis != 15300 || got[0].Fetches != 3 || got[0].Probes != 12 {
		t.Errorf("spend = %+v, want what was recorded", got[0])
	}
	if got[0].Analyzed != 3 || got[0].Discovered != 40 {
		t.Errorf("yield = %+v, want what was recorded", got[0])
	}
}

// A crawl with nothing outstanding is a different state from one working at depth 0, and the
// same absent-versus-zero rule applies here as everywhere else.
func TestACycleWithNothingOutstandingSaysSoRatherThanReportingDepthZero(t *testing.T) {
	s := newTestStore(t)

	if err := s.RecordCycle(CycleSpend{
		Cycle: 1, StartedAt: "2026-09-07T00:00:00Z", HasOutstanding: false,
	}); err != nil {
		t.Fatalf("RecordCycle: %v", err)
	}

	got, err := s.RecentCycles(1)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if got[0].HasOutstanding {
		t.Error("a complete closure reported outstanding work")
	}
}

// Most recent first, because an operator asking what the crawl is spending means now.
func TestRecentCyclesAreNewestFirstAndBounded(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 5; i++ {
		if err := s.RecordCycle(CycleSpend{
			Cycle: i, StartedAt: "2026-09-07T00:00:00Z", Millis: int64(i),
		}); err != nil {
			t.Fatalf("RecordCycle(%d): %v", i, err)
		}
	}

	got, err := s.RecentCycles(3)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d cycles, want the 3 asked for", len(got))
	}
	if got[0].Cycle != 5 || got[2].Cycle != 3 {
		t.Errorf("cycles = %d,%d,%d, want 5,4,3", got[0].Cycle, got[1].Cycle, got[2].Cycle)
	}
}

// A restarted process starts counting cycles at 1 again, so the cycle number cannot be the
// identity or the second run would overwrite the first run's history.
func TestARestartedProcessDoesNotOverwriteEarlierCycles(t *testing.T) {
	s := newTestStore(t)

	for range 2 {
		if err := s.RecordCycle(CycleSpend{
			Cycle: 1, StartedAt: "2026-09-07T00:00:00Z", Millis: 100,
		}); err != nil {
			t.Fatalf("RecordCycle: %v", err)
		}
	}

	got, err := s.RecentCycles(10)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d rows, want both runs' first cycles kept", len(got))
	}
}

// The spend has to be readable as a total, or "under budget" still cannot be checked.
func TestSpendCanBeTotalledAcrossCycles(t *testing.T) {
	s := newTestStore(t)

	for i := 1; i <= 3; i++ {
		if err := s.RecordCycle(CycleSpend{
			Cycle: i, StartedAt: "2026-09-07T00:00:00Z", Millis: 1000,
			Fetches: 2, Probes: 5, Analyzed: 1,
		}); err != nil {
			t.Fatalf("RecordCycle(%d): %v", i, err)
		}
	}

	total, err := s.CycleTotals()
	if err != nil {
		t.Fatalf("CycleTotals: %v", err)
	}
	if total.Cycles != 3 {
		t.Errorf("Cycles = %d, want 3", total.Cycles)
	}
	if total.Millis != 3000 || total.Fetches != 6 || total.Probes != 15 {
		t.Errorf("totals = %+v, want three cycles' worth", total)
	}
}

// Nothing recorded is not zero spend. A fresh database has never run a cycle, and reporting
// 0 ms would read as a crawl that ran and cost nothing.
func TestAFreshDatabaseReportsNoCyclesRatherThanZeroSpend(t *testing.T) {
	s := newTestStore(t)

	total, err := s.CycleTotals()
	if err != nil {
		t.Fatalf("CycleTotals: %v", err)
	}
	if total.Cycles != 0 {
		t.Errorf("Cycles = %d, want 0", total.Cycles)
	}
	got, err := s.RecentCycles(10)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d cycles from a database that has never run one", len(got))
	}
}
