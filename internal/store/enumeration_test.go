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

func declareHasGhas(t *testing.T, s *Store, repositories ...string) {
	t.Helper()
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "has-ghas", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	for _, name := range repositories {
		if _, err := s.InsertOwnedRepository(name); err != nil {
			t.Fatal(err)
		}
	}
}

// Only a full listing establishes absence, so only a full listing names a repository missing, and
// it says when. What it names stops being the operator's, since a declared account covers what the
// account holds and the account no longer holds it.
func TestAFullListingNamesWhatIsMissingAndWhen(t *testing.T) {
	s := newTestStore(t)
	declareHasGhas(t, s, "has-ghas/app", "has-ghas/vanished")

	missing, err := s.RecordRepositoryListing("has-ghas", []string{"has-ghas/app"}, false)
	if err != nil || missing != 0 {
		t.Fatalf("a sweep reported %d missing (%v); a prefix cannot say anything is gone", missing, err)
	}
	if owned, _ := s.OwnedRepositories(); len(owned) != 2 {
		t.Fatalf("after a sweep the owned repositories are %v, want both still", owned)
	}

	missing, err = s.RecordRepositoryListing("has-ghas", []string{"HAS-GHAS/app"}, true)
	if err != nil || missing != 1 {
		t.Fatalf("a full listing reported %d missing (%v), want 1", missing, err)
	}
	state, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Missing) != 1 || state.Missing[0].Repository != "has-ghas/vanished" || state.Missing[0].MissingAt.IsZero() {
		t.Fatalf("the account names %+v missing, want has-ghas/vanished with when", state.Missing)
	}
	owned, err := s.OwnedRepositories()
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 1 || owned[0] != "has-ghas/app" {
		t.Errorf("owned repositories are %v, want only has-ghas/app", owned)
	}
	if origin, err := s.RepositoryOrigin("has-ghas/vanished"); err != nil || origin != OriginSeed {
		t.Errorf("the missing repository's provenance reads %q (%v), want its seed evidence kept", origin, err)
	}

	// How long it has been gone is when it first went missing, not the last listing to say so.
	first := state.Missing[0].MissingAt
	time.Sleep(2 * time.Millisecond)
	if _, err := s.RecordRepositoryListing("has-ghas", []string{"has-ghas/app"}, true); err != nil {
		t.Fatal(err)
	}
	again, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Missing) != 1 || !again.Missing[0].MissingAt.Equal(first) {
		t.Errorf("a second full listing moved when it went missing from %v to %+v", first, again.Missing)
	}
}

// A repository listed again, by a sweep or a full listing, is present: seeing it is evidence, where
// not seeing it in a prefix is not. It is the operator's again with nothing to rebuild.
func TestARepositoryListedAgainIsNoLongerMissing(t *testing.T) {
	s := newTestStore(t)
	declareHasGhas(t, s, "has-ghas/app", "has-ghas/vanished")
	if _, err := s.RecordRepositoryListing("has-ghas", []string{"has-ghas/app"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordRepositoryListing("has-ghas", []string{"has-ghas/vanished"}, false); err != nil {
		t.Fatal(err)
	}
	state, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Missing) != 0 {
		t.Errorf("a repository a sweep saw is still named missing: %+v", state.Missing)
	}
	if owned, _ := s.OwnedRepositories(); len(owned) != 2 {
		t.Errorf("owned repositories are %v, want both again", owned)
	}
	if state.Missing == nil {
		t.Error("an account missing nothing names nil, which marshals to null")
	}
}

// Missing is about what is declared. A repository the declaration no longer covers is not one the
// operator is being told they lost, whatever an earlier listing recorded about it.
func TestARepositoryNoLongerDeclaredIsNotNamedMissing(t *testing.T) {
	s := newTestStore(t)
	declareHasGhas(t, s, "has-ghas/app", "has-ghas/vanished")
	if _, err := s.RecordRepositoryListing("has-ghas", []string{"has-ghas/app"}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "has-ghas", Kind: AccountOrganization,
		Enabled: true, IncludesOnly: true, Includes: []string{"app"}}}, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	state, _, err := s.EnumerationState("has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Missing) != 0 {
		t.Errorf("the account names %+v missing after the declaration stopped covering it", state.Missing)
	}
}
