package store

import (
	"reflect"
	"testing"
)

// The mark names the walk stored reach answers for, so a rolled-back derivation must not leave it
// claiming rows that were never written.
func TestARolledBackDerivationLeavesTheReachMarkAsItWas(t *testing.T) {
	var s Backend = newTestStore(t)
	before, known, err := s.ReadReachMark()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.BeginDerivation()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.MarkReach("rolled-back", map[int64]ReachInput{}, true); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	after, stillKnown, err := s.ReadReachMark()
	if err != nil || known != stillKnown || !reflect.DeepEqual(before, after) {
		t.Fatalf("rollback changed the stored mark: %+v, %v -> %+v, %v (%v)",
			before, known, after, stillKnown, err)
	}
}
