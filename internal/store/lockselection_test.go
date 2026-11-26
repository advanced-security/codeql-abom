package store

import "testing"

// Discovery asks this for every repository it settles, on either backend; PostgreSQL answers the
// lock question as a boolean where SQLite answers 0 or 1.
func TestARepositoryNoLockGovernsSelectsNothing(t *testing.T) {
	s := newTestStore(t)

	selected, err := s.LockSelectedDependencies("octo/tool", "0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 0 {
		t.Errorf("no lock governs octo/tool, so nothing is selected; got %v", selected)
	}
}
