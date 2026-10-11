package store

import (
	"path/filepath"
	"testing"
)

// unitCoverageFixture puts two repositories in one unit and one in another, with the frontier
// saying a single depth-0 row is done, so coverage per unit is not the same number as size.
//
//	octo-org/paid     business_id=3  analyzed
//	octo-org/pending  business_id=3  queued, never analyzed
//	octo-org/other    business_id=7  analyzed
func unitCoverageFixture(t *testing.T) *Store {
	t.Helper()
	s, err := openTestStore(t, filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	for _, r := range []struct {
		name, unit, status string
	}{
		{"octo-org/paid", "3", "done"},
		{"octo-org/pending", "3", "pending"},
		{"octo-org/other", "7", "done"},
	} {
		if _, err := s.UpsertRepository(r.name, ""); err != nil {
			t.Fatalf("UpsertRepository(%s): %v", r.name, err)
		}
		if err := s.ReplacePropertyValues(r.name, []PropertyValue{
			{Name: "business_id", Value: r.unit},
		}); err != nil {
			t.Fatalf("ReplacePropertyValues(%s): %v", r.name, err)
		}
		// Depth zero is what makes a frontier row a repository rather than one of its
		// dependencies, which is the same reading TierCounts uses.
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
			VALUES (?, '', 'octo-org', ?, 0, ?, '2026-01-01T00:00:00Z')`,
			r.name, r.name[len("octo-org/"):], r.status); err != nil {
			t.Fatalf("seeding the frontier for %s: %v", r.name, err)
		}
	}
	return s
}

// A unit's size and how much of it has been looked at are different questions, and a coverage
// figure that reports size answers neither. Eight repositories in a unit with two analyzed is the
// state an owner needs to see before reading any finding count as representative.
func TestAUnitReportsHowMuchOfItHasBeenAnalyzed(t *testing.T) {
	s := unitCoverageFixture(t)

	breakdown, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatalf("PropertyBreakdown: %v", err)
	}

	byValue := map[string]PropertyCount{}
	for _, v := range breakdown.Values {
		byValue[v.Value] = v
	}
	if got := byValue["3"]; got.Repositories != 2 || got.Analyzed != 1 {
		t.Errorf("unit 3 reports %d of %d analyzed, want 1 of 2",
			got.Analyzed, got.Repositories)
	}
	if got := byValue["7"]; got.Repositories != 1 || got.Analyzed != 1 {
		t.Errorf("unit 7 reports %d of %d analyzed, want 1 of 1",
			got.Analyzed, got.Repositories)
	}
}

// A repository nothing has queued has not been examined and found clean; it has not been examined.
// Counting it as analyzed would make an untouched unit indistinguishable from a finished one.
func TestARepositoryNothingHasQueuedIsNotCountedAsAnalyzed(t *testing.T) {
	s := unitCoverageFixture(t)

	if _, err := s.UpsertRepository("octo-org/untouched", ""); err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	if err := s.ReplacePropertyValues("octo-org/untouched", []PropertyValue{
		{Name: "business_id", Value: "3"},
	}); err != nil {
		t.Fatalf("ReplacePropertyValues: %v", err)
	}

	breakdown, err := s.PropertyBreakdown("business_id")
	if err != nil {
		t.Fatalf("PropertyBreakdown: %v", err)
	}
	for _, v := range breakdown.Values {
		if v.Value != "3" {
			continue
		}
		if v.Repositories != 3 || v.Analyzed != 1 {
			t.Errorf("unit 3 reports %d of %d analyzed, want 1 of 3: a repository with no "+
				"frontier row counts toward the unit and not toward what was examined",
				v.Analyzed, v.Repositories)
		}
	}
}
