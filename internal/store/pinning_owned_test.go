package store

import "testing"

// The declaration marks rows; it must never remove them. A reader has to be able to see the
// whole corpus and their part of it at the same time.
func TestOwnedSplitLeavesTheUndividedTotalIntact(t *testing.T) {
	s := subgraphFixture(t)

	before, err := s.PinningTotals(PinningFilter{})
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}

	if err := s.SetOwnedOrgs([]string{"octo-org"}); err != nil {
		t.Fatalf("SetOwnedOrgs: %v", err)
	}
	after, err := s.PinningTotals(PinningFilter{})
	if err != nil {
		t.Fatalf("PinningTotals after declaring: %v", err)
	}

	if before.Pinnable != after.Pinnable || before.Flagged != after.Flagged {
		t.Errorf("declaring an owner changed the totals: %d/%d became %d/%d",
			before.Pinnable, before.Flagged, after.Pinnable, after.Flagged)
	}
}

// Yours plus elsewhere has to equal the whole, or the interface is asserting something the data
// does not support.
func TestOwnedSplitReconcilesToTheTotal(t *testing.T) {
	s := subgraphFixture(t)
	if err := s.SetOwnedOrgs([]string{"octo-org"}); err != nil {
		t.Fatalf("SetOwnedOrgs: %v", err)
	}

	total, err := s.PinningTotals(PinningFilter{})
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}
	if total.Yours.Total() != total.Pinnable {
		t.Errorf("pinnable split %d + %d = %d, want %d",
			total.Yours.Yours, total.Yours.Elsewhere, total.Yours.Total(), total.Pinnable)
	}
	if total.FlaggedSplit.Total() != total.Flagged {
		t.Errorf("flagged split %d + %d = %d, want %d", total.FlaggedSplit.Yours,
			total.FlaggedSplit.Elsewhere, total.FlaggedSplit.Total(), total.Flagged)
	}
}

// Rows carry the mark so a reader can sort and scan by it.
func TestPinningRowsAreMarkedOwned(t *testing.T) {
	s := subgraphFixture(t)
	if err := s.SetOwnedOrgs([]string{"octo-org"}); err != nil {
		t.Fatalf("SetOwnedOrgs: %v", err)
	}

	rows, err := s.PinningLevel(PinningByOwner, "", PinningFilter{}, 0)
	if err != nil {
		t.Fatalf("PinningLevel: %v", err)
	}
	var marked int
	for _, r := range rows {
		if r.Owned {
			marked++
			if r.ScopeKey != "octo-org" {
				t.Errorf("%s is marked owned but was never declared", r.ScopeKey)
			}
		}
	}
	if marked == 0 {
		t.Error("declared octo-org, no owner row is marked")
	}
}

// With nothing declared, nothing is marked and both halves of every split read as elsewhere.
func TestNoDeclarationMarksNothing(t *testing.T) {
	s := subgraphFixture(t)

	rows, err := s.PinningLevel(PinningByOwner, "", PinningFilter{}, 0)
	if err != nil {
		t.Fatalf("PinningLevel: %v", err)
	}
	for _, r := range rows {
		if r.Owned {
			t.Errorf("%s is marked owned with no declaration", r.ScopeKey)
		}
	}

	total, err := s.PinningTotals(PinningFilter{})
	if err != nil {
		t.Fatalf("PinningTotals: %v", err)
	}
	if total.Yours.Yours != 0 {
		t.Errorf("nothing declared, yet %d references count as yours", total.Yours.Yours)
	}
	if total.Yours.Elsewhere != total.Pinnable {
		t.Errorf("undeclared corpus should all be elsewhere: %d of %d",
			total.Yours.Elsewhere, total.Pinnable)
	}
}
