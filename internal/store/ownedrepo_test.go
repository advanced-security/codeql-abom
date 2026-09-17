package store

import "testing"

// Enumerating a declared account is what puts the operator's own repositories in the corpus. On
// the reference corpus they owned 397 and it held 1 — and that one was marked `upstream`, because
// it arrived only as somebody else's dependency.
func TestAnEnumeratedRepositoryIsRecordedAsTheOperatorsOwn(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.InsertOwnedRepository("advanced-security/dismiss-alerts"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}

	if got := originOf(t, s, "advanced-security/dismiss-alerts"); got != OriginSeed {
		t.Errorf("origin = %q, want %q", got, OriginSeed)
	}
}

// The one repository the operator owned was already present as `upstream`. Declaring the account
// is new information about it, so enumeration promotes rather than skipping.
func TestEnumerationPromotesARepositoryFirstSeenAsSomebodyElsesDependency(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.InsertUpstreamRepository("advanced-security/dismiss-alerts"); err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	if _, err := s.InsertOwnedRepository("advanced-security/dismiss-alerts"); err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}

	if got := originOf(t, s, "advanced-security/dismiss-alerts"); got != OriginSeed {
		t.Errorf("origin = %q, want the declaration to promote it to %q", got, OriginSeed)
	}
}

// Row identity must survive the promotion: every node and edge already points at this row, and a
// second row for the same name would strand all of it.
func TestPromotingKeepsTheSameRepositoryRow(t *testing.T) {
	s := newTestStore(t)
	before, err := s.InsertUpstreamRepository("advanced-security/dismiss-alerts")
	if err != nil {
		t.Fatalf("InsertUpstreamRepository: %v", err)
	}

	after, err := s.InsertOwnedRepository("advanced-security/dismiss-alerts")
	if err != nil {
		t.Fatalf("InsertOwnedRepository: %v", err)
	}

	if before != after {
		t.Errorf("row id %d became %d; everything already recorded points at the first",
			before, after)
	}
}

func originOf(t *testing.T, s *Store, fullName string) string {
	t.Helper()
	var origin string
	if err := s.db.QueryRow(
		`SELECT origin FROM repository WHERE repository_full_name = ?`, fullName,
	).Scan(&origin); err != nil {
		t.Fatalf("reading origin of %q: %v", fullName, err)
	}
	return origin
}
