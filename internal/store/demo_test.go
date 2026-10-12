package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// A demo database holds practice requests and synthetic ownership; any ordinary command that
// opened it would treat that as an operator's real estate and act on it.
func TestMarkedDemoDatabaseIsRefusedWithoutDemoStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := openTestStore(t, path, Options{Demo: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	want := DemoDataset{ID: "demo-1", Scenario: "2026.1"}
	if err := s.MarkDemo(want); err != nil {
		t.Fatalf("MarkDemo: %v", err)
	}
	_ = s.Close()

	for _, opts := range []Options{{}, {ReadOnly: true}} {
		if s, err := openTestStore(t, path, opts); !errors.Is(err, ErrDemoDatabase) {
			if s != nil {
				_ = s.Close()
			}
			t.Errorf("opening a demo database with %+v: %v, want ErrDemoDatabase", opts, err)
		}
	}

	s, err = openTestStore(t, path, Options{Demo: true})
	if err != nil {
		t.Fatalf("opening for demo startup: %v", err)
	}
	defer s.Close()
	got, marked, err := s.DemoDataset()
	if err != nil || !marked || got.ID != want.ID || got.Scenario != want.Scenario || got.MarkedAt == "" {
		t.Fatalf("DemoDataset = %+v, %v, %v; want %+v marked", got, marked, err, want)
	}
}

// Demo startup over an ordinary database must not quietly adopt it as practice data.
func TestOrdinaryDatabaseIsNotADemo(t *testing.T) {
	s := newTestStore(t)
	got, marked, err := s.DemoDataset()
	if err != nil || marked || got != (DemoDataset{}) {
		t.Fatalf("DemoDataset on an ordinary database = %+v, %v, %v", got, marked, err)
	}
}

// One database is one dataset: a second identity would let old browser progress claim new data.
func TestDemoMarkerIsWrittenOnce(t *testing.T) {
	s := newTestStore(t)
	if err := s.MarkDemo(DemoDataset{ID: "first", Scenario: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDemo(DemoDataset{ID: "second", Scenario: "1"}); err == nil {
		t.Fatal("a second demo marker was accepted")
	}
	if err := s.MarkDemo(DemoDataset{Scenario: "1"}); err == nil {
		t.Fatal("a marker without a dataset identity was accepted")
	}
	got, _, err := s.DemoDataset()
	if err != nil || got.ID != "first" {
		t.Fatalf("DemoDataset = %+v, %v; want the first marker kept", got, err)
	}
}
