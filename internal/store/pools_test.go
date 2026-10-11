package store

import (
	"path/filepath"
	"testing"
	"time"
)

// The rate an operator sets is configuration, so a restart must not quietly drop it back to none.
func TestAPoolRateSurvivesReopeningAndClearingRemovesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	open := func() *Store {
		s, err := openTestStore(t, path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(); err != nil {
			t.Fatal(err)
		}
		return s
	}

	s := open()
	if err := s.SetPoolRate(PoolRate{Pool: "reference probing", Unit: "git requests", PerHour: 600, Burst: 5}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s = open()
	defer s.Close()
	got, err := s.PoolRates()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PerHour != 600 || got[0].Burst != 5 || got[0].SetAt == "" {
		t.Fatalf("want 600 an hour with a burst of 5, as set; got %+v", got)
	}

	if err := s.ClearPoolRate("reference probing", "git requests"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PoolRates(); len(got) != 0 {
		t.Errorf("a cleared rate is no rate; got %+v", got)
	}
}

// Zero an hour would hold the work for ever, which is not what removing a limit means: absent and
// zero are different instructions, and only absence is accepted here.
func TestARateOfZeroIsRefusedRatherThanReadAsNone(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetPoolRate(PoolRate{Pool: "fetching", Unit: "git requests", PerHour: 0, Burst: 1}); err == nil {
		t.Error("a rate of zero must be refused")
	}
	if err := s.SetPoolRate(PoolRate{Pool: "fetching", Unit: "git requests", PerHour: 60, Burst: 0}); err == nil {
		t.Error("a burst below one must be refused")
	}
}

// A database that has never spent reports no spend, not a spend of zero.
func TestADatabaseThatNeverSpentReportsNoSpend(t *testing.T) {
	s := newTestStore(t)

	got, err := s.PoolMeters(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("nothing was ever spent, so no pool has a meter; got %#v", got)
	}
}

// Spend adds up within a minute, splits at the hour the meter asks about, and keeps the last thing
// the provider reported even when a later minute carried no reading.
func TestSpendAddsUpAndKeepsTheLatestReading(t *testing.T) {
	s := newTestStore(t)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	reset := now.Add(30 * time.Minute)
	rows := []PoolSpend{
		{Pool: "enumeration", Unit: "REST requests", Bucket: now.Add(-2 * time.Hour), Requests: 10, Charged: 10},
		{Pool: "enumeration", Unit: "REST requests", Bucket: now.Add(-time.Minute), Requests: 3, Charged: 2,
			HasReading: true, Remaining: 4990, Limit: 5000, ResetAt: reset},
		{Pool: "enumeration", Unit: "REST requests", Bucket: now, Requests: 1, Charged: 1},
	}
	if err := s.AddPoolSpend(rows); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPoolSpend(rows[2:]); err != nil {
		t.Fatal(err)
	}

	got, err := s.PoolMeters(now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("one pool spent; got %+v", got)
	}
	m := got[0]
	if m.LastHour.Requests != 5 || m.LastHour.Charged != 4 {
		t.Errorf("the last hour holds 3+1+1 requests, 2+1+1 charged; got %+v", m.LastHour)
	}
	if m.Total.Requests != 15 || m.Total.Charged != 14 {
		t.Errorf("in all, 15 requests and 14 charged; got %+v", m.Total)
	}
	if m.Reported == nil || m.Reported.Remaining != 4990 || !m.Reported.ResetAt.Equal(reset) {
		t.Errorf("the latest reading survives a later minute that carried none; got %+v", m.Reported)
	}
}
