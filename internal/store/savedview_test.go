package store

import "testing"

// A stored view has to come back as the same question it went in as.
//
// The canonical form is the contract: the browser, the API and the gate all read the same text,
// so a view stored verbatim would let two spellings of one question compare unequal and a typo
// survive until whichever consumer parsed it last.
func TestASavedViewIsStoredInItsCanonicalForm(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveView(SavedView{
		Name: "my-closure", Predicate: "flags=findings&scope=owned",
		CreatedBy: DeclaredByInterface,
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	got, ok, err := s.SavedViewNamed("my-closure")
	if err != nil {
		t.Fatalf("SavedViewNamed: %v", err)
	}
	if !ok {
		t.Fatal("a view that was just saved could not be found")
	}
	// Keys sort ordinally, so scope precedes flags whichever order they arrived in.
	if got.Predicate != "flags=findings&scope=owned" {
		t.Errorf("Predicate = %q, want the canonical spelling", got.Predicate)
	}
}

// A predicate that cannot be parsed must be refused when it is written, not when it is read.
// Storing one moves the failure to whoever loads it next, which is the gate in the middle of a
// pull request.
func TestAViewNamingSomethingThatIsNotAPredicateIsRefused(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveView(SavedView{Name: "broken", Predicate: "scoop=seed"}); err == nil {
		t.Error("a view naming an unknown filter was accepted")
	}
	if _, ok, _ := s.SavedViewNamed("broken"); ok {
		t.Error("the refused view was stored anyway")
	}
}

// How a view arrived is recorded by the code path that wrote it, never inferred from its payload,
// and NULL stays NULL. The same rule as abom_owned_scope.declared_by.
func TestASavedViewRecordsHowItArrivedAndNeverGuesses(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveView(SavedView{Name: "anonymous", Predicate: "scope=owned"}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	got, _, err := s.SavedViewNamed("anonymous")
	if err != nil {
		t.Fatalf("SavedViewNamed: %v", err)
	}
	if got.CreatedBy != "" {
		t.Errorf("CreatedBy = %q, want the empty string that means nobody recorded it", got.CreatedBy)
	}
	if got.CreatedAt == "" {
		t.Error("a stored view has no record of when it arrived")
	}
}

// Saving the same name twice replaces the question rather than accumulating copies of it — the
// defect the observation table carried for months.
func TestSavingAViewTwiceReplacesIt(t *testing.T) {
	s := newTestStore(t)

	for _, predicate := range []string{"scope=owned", "scope=exposure"} {
		if err := s.SaveView(SavedView{Name: "mine", Predicate: predicate}); err != nil {
			t.Fatalf("SaveView(%s): %v", predicate, err)
		}
	}

	views, err := s.SavedViews()
	if err != nil {
		t.Fatalf("SavedViews: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("saving one name twice left %d views", len(views))
	}
	if views[0].Predicate != "scope=exposure" {
		t.Errorf("Predicate = %q, want the one written last", views[0].Predicate)
	}
}

// A built-in belongs to the product. Letting an operator take the name over would mean the same
// view answered two different questions depending on which database it was asked on, and a gate
// policy naming it would silently change meaning.
func TestABuiltInNameCannotBeTakenOverOrDeleted(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveView(SavedView{Name: "owned-findings", Predicate: "scope=exposure"}); err == nil {
		t.Error("a built-in view was overwritten by an operator's own")
	}
	if err := s.DeleteView("advisory-routes"); err == nil {
		t.Error("a built-in view was deleted")
	}
}

func TestDeletingASavedViewRemovesIt(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveView(SavedView{Name: "temporary", Predicate: "scope=owned"}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	if err := s.DeleteView("temporary"); err != nil {
		t.Fatalf("DeleteView: %v", err)
	}
	if _, ok, _ := s.SavedViewNamed("temporary"); ok {
		t.Error("a deleted view is still on record")
	}
}
