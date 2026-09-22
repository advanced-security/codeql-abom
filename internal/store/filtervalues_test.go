package store

import "testing"

// A reader cannot filter by a rule they have to know the name of. The control offered a free-text
// box against `actions/unpinned-tag`-shaped identifiers, which is a memory test rather than a
// filter, so the values a term can actually take have to be readable from the corpus.
//
// Counted rather than merely listed: 11,806 findings across 21 rules is a list in which two or
// three rules are the whole picture, and a plain alphabetical list hides that.
func TestTheRulesOnOfferAreTheOnesThatReported(t *testing.T) {
	s := matchFixture(t)

	values, err := s.FilterValues("rules")
	if err != nil {
		t.Fatalf("FilterValues: %v", err)
	}
	if len(values.Values) == 0 {
		t.Fatal("no rules on offer, though the fixture seeded findings")
	}

	seen := map[string]int{}
	for _, v := range values.Values {
		if v.Count <= 0 {
			t.Errorf("rule %q offered with a count of %d", v.Value, v.Count)
		}
		seen[v.Value] = v.Count
	}
	if _, ok := seen["actions/unpinned-tag"]; !ok {
		t.Errorf("the seeded rule is not on offer; got %v", seen)
	}
}

// Most-used first. A rule that reported once and a rule that reported ten thousand times are not
// equally likely to be what somebody came to filter by, and alphabetical order says they are.
func TestTheRulesOnOfferLeadWithTheOnesThatReportedMost(t *testing.T) {
	s := matchFixture(t)

	values, err := s.FilterValues("rules")
	if err != nil {
		t.Fatalf("FilterValues: %v", err)
	}
	for i := 1; i < len(values.Values); i++ {
		if values.Values[i-1].Count < values.Values[i].Count {
			t.Errorf("rule %q (%d) is offered before %q (%d)",
				values.Values[i-1].Value, values.Values[i-1].Count, values.Values[i].Value, values.Values[i].Count)
		}
	}
}

// A term whose values are not the store's to know is refused by name rather than answered with an
// empty list. Empty reads as "the corpus contains none", which is a measurement nobody took.
func TestATermWithNoCorpusDerivedValuesIsRefusedRatherThanAnsweredEmpty(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.FilterValues("scope"); err == nil {
		t.Error("a term with a fixed vocabulary was answered from the corpus")
	}
}

// A column nothing recorded a value in leaves rows the term cannot speak for, and the count of
// them is the difference between an honest filter and one that quietly answers for a fraction.
//
// Provider is the live case: most findings predate the column, are correctly left out of the value
// list rather than relabelled, and are invisible unless this number travels with it.
func TestAValueListSaysHowManyRowsItCannotSpeakFor(t *testing.T) {
	s := matchFixture(t)
	if _, err := s.DB().Exec(`UPDATE abom_finding SET provider = '' WHERE rowid % 2 = 0`); err != nil {
		t.Fatalf("clearing providers: %v", err)
	}

	set, err := s.FilterValues("provider")
	if err != nil {
		t.Fatalf("FilterValues: %v", err)
	}

	var counted int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_finding WHERE provider IS NULL OR provider = ''`).
		Scan(&counted); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if counted == 0 {
		t.Fatal("the fixture left nothing unattributed, so this measures nothing")
	}
	if set.Uncovered != counted {
		t.Errorf("Uncovered = %d, want the %d rows recording no provider", set.Uncovered, counted)
	}
}

// A term every row carries leaves nothing out, and must say zero rather than omit the number:
// zero here is a measurement, and it is what tells a reader the list is the whole population.
func TestATermEveryRowCarriesLeavesNothingOut(t *testing.T) {
	s := matchFixture(t)

	set, err := s.FilterValues("rules")
	if err != nil {
		t.Fatalf("FilterValues: %v", err)
	}
	if set.Uncovered != 0 {
		t.Errorf("Uncovered = %d, want none: every finding carries a rule", set.Uncovered)
	}
}

// A corpus that has produced no findings offers no rules, which is different from the term being
// unknown. The caller has to be able to tell them apart.
func TestACorpusWithNoFindingsOffersNoRulesWithoutFailing(t *testing.T) {
	s := newTestStore(t)

	values, err := s.FilterValues("rules")
	if err != nil {
		t.Fatalf("FilterValues on an empty corpus: %v", err)
	}
	if len(values.Values) != 0 {
		t.Errorf("got %d rules from a corpus with no findings", len(values.Values))
	}
}

// How many values a term has is worth knowing before opening it: a filter with one value narrows
// nothing, and a reader should not have to open nineteen menus to find that out.
func TestHowManyValuesATermHasIsCountedWithoutReadingThemAll(t *testing.T) {
	s := matchFixture(t)

	counts, err := s.FilterValueCounts()
	if err != nil {
		t.Fatalf("FilterValueCounts: %v", err)
	}

	rules, err := s.FilterValues("rules")
	if err != nil {
		t.Fatalf("FilterValues: %v", err)
	}
	if counts["rules"] != len(rules.Values) {
		t.Errorf("counted %d rules but listing them gives %d", counts["rules"], len(rules.Values))
	}
}

// A term nobody counted is absent, never zero. Zero says "this filter has nothing to offer", which
// would send a reader past a filter that works perfectly well on values the store does not hold.
func TestATermWhoseValuesTheStoreDoesNotHoldIsAbsentRatherThanZero(t *testing.T) {
	s := matchFixture(t)

	counts, err := s.FilterValueCounts()
	if err != nil {
		t.Fatalf("FilterValueCounts: %v", err)
	}
	if _, present := counts["q"]; present {
		t.Error("free text was counted, though its values are whatever somebody types")
	}
}

// A term the corpus genuinely has no values for is counted as zero and said so. That is a
// measurement — nothing has reported — and it is what lets a control rank it down honestly.
func TestATermWithNothingInTheCorpusIsCountedZeroRatherThanOmitted(t *testing.T) {
	s := newTestStore(t)

	counts, err := s.FilterValueCounts()
	if err != nil {
		t.Fatalf("FilterValueCounts: %v", err)
	}
	if got, present := counts["rules"]; !present || got != 0 {
		t.Errorf("rules counted %d (present %v), want a measured zero", got, present)
	}
}
