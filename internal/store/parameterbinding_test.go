package store

import "testing"

func TestNumberedParametersRetainSQLiteSlotsIncludingUnusedOnes(t *testing.T) {
	s := newTestStore(t)
	query := `SELECT ?3, ?1, ?3, ?`
	check := func(scan func(...any) error) {
		t.Helper()
		var a, b, c, d int
		if err := scan(&a, &b, &c, &d); err != nil {
			t.Fatal(err)
		}
		if a != 3 || b != 1 || c != 3 || d != 4 {
			t.Fatalf("numbered slots changed: %d %d %d %d", a, b, c, d)
		}
	}
	check(s.db.QueryRow(query, 1, "unused", 3, 4).Scan)
	stmt, err := s.db.Prepare(query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	check(stmt.QueryRow(1, "unused", 3, 4).Scan)
}
