package store

import (
	"reflect"
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

// What code scanning supplied is what saved a build or an evaluation, so a cycle keeps it beside
// what it spent, refusals included, or Settings cannot say why a database was built anyway.
func TestACycleKeepsWhatCodeScanningSuppliedAndWhyItRefusedTheRest(t *testing.T) {
	s := newTestStore(t)
	took := CycleCodeScanning{Databases: 2, Analyses: 1,
		Refused: map[string]int{"database: another commit": 3, "analysis: no analysis": 1}}
	if err := s.RecordCycle(CycleSpend{Cycle: 1, StartedAt: "2026-10-07T00:00:00Z", Builds: 3,
		CodeScanning: &took}); err != nil {
		t.Fatalf("RecordCycle: %v", err)
	}
	if err := s.RecordCycle(CycleSpend{Cycle: 2, StartedAt: "2026-10-07T00:01:00Z",
		CodeScanning: &CycleCodeScanning{Refused: map[string]int{}}}); err != nil {
		t.Fatalf("RecordCycle: %v", err)
	}

	got, err := s.RecentCycles(2)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if len(got) != 2 || got[1].CodeScanning == nil || !reflect.DeepEqual(*got[1].CodeScanning, took) {
		t.Fatalf("the first cycle reads back %+v, want %+v", got, took)
	}
	if c := got[0].CodeScanning; c == nil || c.Databases != 0 || c.Analyses != 0 || c.Refused == nil ||
		len(c.Refused) != 0 {
		t.Errorf("a cycle code scanning supplied nothing in reads back %+v, want zero and no refusal", c)
	}
}

// A cycle recorded before ABOM kept what code scanning supplied says nothing about it, which is not
// a cycle that took nothing; totals count only the cycles that kept it, and say how many those are.
func TestCycleTotalsCountWhatCodeScanningSuppliedOnlyWhereItWasKept(t *testing.T) {
	s := newTestStore(t)
	if err := s.RecordCycle(CycleSpend{Cycle: 1, StartedAt: "2026-10-06T00:00:00Z", Builds: 4}); err != nil {
		t.Fatalf("RecordCycle: %v", err)
	}
	for i, c := range []CycleCodeScanning{
		{Databases: 1, Refused: map[string]int{"database: another commit": 2}},
		{Databases: 1, Analyses: 2, Refused: map[string]int{"database: another commit": 1, "database: no database": 4}},
	} {
		if err := s.RecordCycle(CycleSpend{Cycle: i + 2, StartedAt: "2026-10-07T00:00:00Z",
			CodeScanning: &c}); err != nil {
			t.Fatalf("RecordCycle: %v", err)
		}
	}

	got, err := s.RecentCycles(3)
	if err != nil {
		t.Fatalf("RecentCycles: %v", err)
	}
	if got[2].CodeScanning != nil {
		t.Errorf("a cycle recorded without it reads back %+v, want nothing recorded", got[2].CodeScanning)
	}
	total, err := s.CycleTotals()
	if err != nil {
		t.Fatalf("CycleTotals: %v", err)
	}
	want := CycleCodeScanning{Databases: 2, Analyses: 2,
		Refused: map[string]int{"database: another commit": 3, "database: no database": 4}}
	if total.Cycles != 3 || total.CodeScanningCycles != 2 || !reflect.DeepEqual(total.CodeScanning, want) {
		t.Errorf("totals read %d cycles, %d keeping code scanning's answers, and %+v; want 3, 2 and %+v",
			total.Cycles, total.CodeScanningCycles, total.CodeScanning, want)
	}
}

// A fresh database has kept nothing from code scanning, which an empty refusal list says without
// becoming null for a client reading it.
func TestAFreshDatabaseTotalsNoCodeScanningAnswers(t *testing.T) {
	s := newTestStore(t)
	total, err := s.CycleTotals()
	if err != nil {
		t.Fatalf("CycleTotals: %v", err)
	}
	if total.CodeScanningCycles != 0 || total.CodeScanning.Refused == nil {
		t.Errorf("a fresh database totals %+v, want no cycles and an empty refusal list", total)
	}
}
