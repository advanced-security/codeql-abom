package store

import (
	"reflect"
	"testing"
)

func TestStoreTextOrdersByBytesUnlessCaseInsensitivityIsDeclared(t *testing.T) {
	s := newTestStore(t)
	names := []string{"actions/untrusted-checkout-toctou/high", "actions/untrusted-checkout/high"}
	for _, name := range names {
		if _, err := s.UpsertRepository(name, ""); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query(`SELECT repository_full_name FROM repository ORDER BY repository_full_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("text order=%v, want byte order=%v", got, names)
	}
}
