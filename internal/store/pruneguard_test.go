package store

import (
	"errors"
	"testing"
)

// The state that made this necessary: an account was declared and enumerated, which gave it
// repositories, and no analysis had run yet, which gave it no edges. A closure with no edges
// reaches nothing, so everything falls outside it.
//
// Nothing about that is a measurement. It is the same absence the empty-closure refusal already
// covers, arriving one step later.
func TestPruningIsRefusedWhileTheDeclaredClosureHasNoAnalysisBehindIt(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.UpsertRepository("octo-org/fresh", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	_, err := s.ExplainOutsideClosure([]string{"octo-org/fresh"})

	if !errors.Is(err, ErrUnanalyzedClosure) {
		t.Errorf("err = %v, want a refusal naming the closure as unanalyzed", err)
	}
}

// The refusal has to hold on the half that acts, not only on the half that explains, or the
// interface refuses to describe a prune it would still carry out.
func TestMarkingIsRefusedOnAnUnanalyzedClosureToo(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.UpsertRepository("octo-org/fresh", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	before := countPrune(t, s, `SELECT COUNT(*) FROM abom_node WHERE removed_at IS NULL`)

	if _, err := s.MarkOutsideClosure([]string{"octo-org/fresh"}); !errors.Is(err, ErrUnanalyzedClosure) {
		t.Errorf("err = %v, want a refusal", err)
	}
	if after := countPrune(t, s, `SELECT COUNT(*) FROM abom_node WHERE removed_at IS NULL`); after != before {
		t.Errorf("live nodes %d -> %d: the refusal still removed something", before, after)
	}
}

// And naming what would go is refused for the same reason, because that list is what a checkout
// removal would delete off disk.
func TestNamingIsRefusedOnAnUnanalyzedClosureToo(t *testing.T) {
	s := pruneFixture(t)
	if _, err := s.UpsertRepository("octo-org/fresh", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	if _, err := s.RepositoriesOutsideClosure([]string{"octo-org/fresh"}); !errors.Is(err, ErrUnanalyzedClosure) {
		t.Errorf("err = %v, want a refusal", err)
	}
}

// The ordinary case is untouched: a closure with analysis behind it still prunes what it does not
// reach. A guard that refused everything would be the same defect facing the other way.
func TestAClosureWithAnalysisBehindItStillPrunes(t *testing.T) {
	s := pruneFixture(t)

	report, err := s.ExplainOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("ExplainOutsideClosure: %v", err)
	}
	if report.Nodes == 0 {
		t.Error("an analyzed closure reported nothing outside it, so the guard is too wide")
	}
}
