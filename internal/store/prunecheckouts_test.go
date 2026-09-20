package store

import (
	"errors"
	"slices"
	"testing"
)

// A prune tombstones the rows; the working copies that were fetched to produce them stay on disk
// until something names them. A count cannot drive that — removing a directory needs to know
// which repository it belongs to.
func TestTheRepositoriesAPruneRemovesAreNamedRatherThanOnlyCounted(t *testing.T) {
	s := pruneFixture(t)

	got, err := s.RepositoriesOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("RepositoriesOutsideClosure: %v", err)
	}
	if !slices.Equal(got, []string{"stranger/repo"}) {
		t.Errorf("outside = %v, want the one repository the closure does not reach", got)
	}
}

// The count and the names answer the same question, so they have to agree. Two predicates that
// drift apart would offer a operator a list that does not match the number beside it.
func TestTheNamesAgreeWithTheCountTheReportGives(t *testing.T) {
	s := pruneFixture(t)

	report, err := s.ExplainOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("ExplainOutsideClosure: %v", err)
	}
	named, err := s.RepositoriesOutsideClosure([]string{"octo-org/app"})
	if err != nil {
		t.Fatalf("RepositoriesOutsideClosure: %v", err)
	}
	if len(named) != report.Repositories {
		t.Errorf("named %d repositories and counted %d", len(named), report.Repositories)
	}
}

// The same refusal the prune itself makes. Nothing declared reaches nothing, and naming every
// repository as removable would hand a caller the whole corpus to delete off disk.
func TestNamingWhatIsOutsideAnEmptyClosureIsRefused(t *testing.T) {
	s := pruneFixture(t)

	if _, err := s.RepositoriesOutsideClosure(nil); !errors.Is(err, ErrEmptyClosure) {
		t.Errorf("err = %v, want ErrEmptyClosure", err)
	}
}
