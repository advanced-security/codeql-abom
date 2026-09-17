package store

import (
	"testing"
	"time"
)

// An account with no listing behind it has an unknown shape, not zero repositories. A sweep that
// read "no marks" as "marks of zero" would treat every repository as newer than the high-water and
// call a full listing an incremental one.
func TestAnAccountNobodyListedHasNoMarks(t *testing.T) {
	s := newTestStore(t)

	state, known, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if known {
		t.Error("nobody has listed this account, so there are no marks to read")
	}
	if !state.HighPushed.IsZero() || !state.HighCreated.IsZero() {
		t.Errorf("an unlisted account has no high-water marks; got %+v", state)
	}
}

func TestAFullListingRecordsTheMarksASweepStopsAt(t *testing.T) {
	s := newTestStore(t)
	pushed := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	created := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	if err := s.RecordFullListing("has-ghas", "graphql", 3, created, pushed); err != nil {
		t.Fatal(err)
	}

	state, known, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if !known {
		t.Fatal("a full listing has run, so its marks must be readable")
	}
	if !state.HighPushed.Equal(pushed) || !state.HighCreated.Equal(created) {
		t.Errorf("got created=%v pushed=%v, want %v and %v",
			state.HighCreated, state.HighPushed, created, pushed)
	}
	if state.ListedAt.IsZero() {
		t.Error("a full listing is the only thing that establishes absence, so when it ran is a fact")
	}
	if state.Route != "graphql" {
		t.Errorf("route %q, want the route that answered to be stated rather than inferred", state.Route)
	}
}

// Only a full listing can establish that a repository is gone, so a sweep must never be allowed to
// look like one. Moving ListedAt forward here would make a corpus that has not checked for
// deletions in a week report that it checked a minute ago.
func TestASweepAdvancesTheMarksAndNotTheFullListing(t *testing.T) {
	s := newTestStore(t)
	listed := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := s.RecordFullListing("has-ghas", "rest", 3, listed, listed); err != nil {
		t.Fatal(err)
	}
	before, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}

	moved := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	if err := s.RecordSweep("has-ghas", "graphql", listed, moved); err != nil {
		t.Fatal(err)
	}

	after, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if !after.HighPushed.Equal(moved) {
		t.Errorf("the sweep saw a push at %v; got high-water %v", moved, after.HighPushed)
	}
	if !after.ListedAt.Equal(before.ListedAt) {
		t.Errorf("a sweep is not a full listing; ListedAt moved from %v to %v",
			before.ListedAt, after.ListedAt)
	}
	if after.SweptAt.IsZero() {
		t.Error("when the account was last swept is what makes the staleness visible")
	}
}

// A sweep that found nothing must not drag a mark backwards, or the next one re-reads what this one
// already accounted for and the early stop stops being an early stop.
func TestAMarkNeverMovesBackwards(t *testing.T) {
	s := newTestStore(t)
	high := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	if err := s.RecordFullListing("has-ghas", "graphql", 3, high, high); err != nil {
		t.Fatal(err)
	}

	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.RecordSweep("has-ghas", "graphql", older, older); err != nil {
		t.Fatal(err)
	}

	state, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if !state.HighPushed.Equal(high) || !state.HighCreated.Equal(high) {
		t.Errorf("marks went backwards to created=%v pushed=%v, want both still %v",
			state.HighCreated, state.HighPushed, high)
	}
}

// GitHub matches account names case-insensitively and an operator types whichever they remember.
// Two rows for one account is two sets of marks, and the sweep would read whichever it asked for.
func TestMarksAreOneAccountRegardlessOfSpelling(t *testing.T) {
	s := newTestStore(t)
	high := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	if err := s.RecordFullListing("Has-GHAS", "graphql", 3, high, high); err != nil {
		t.Fatal(err)
	}

	state, known, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if !known || !state.HighPushed.Equal(high) {
		t.Errorf("got known=%v pushed=%v, want the marks written as Has-GHAS", known, state.HighPushed)
	}
}
