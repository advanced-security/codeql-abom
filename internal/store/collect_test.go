package store

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRowCollectionPreservesOrderEmptySetsAndErrors(t *testing.T) {
	s := newTestStore(t)
	scan := func(r *sql.Rows) (int, error) {
		var n int
		err := r.Scan(&n)
		return n, err
	}
	for _, tc := range []struct {
		name, query, problem string
		want                 []int
	}{
		{"ordered", "SELECT 3 UNION ALL SELECT 1", "", []int{3, 1}},
		{"empty", "SELECT 1 WHERE 0", "", []int{}},
		{"query fails", "SELECT absent FROM no_table", "collecting numbers", nil},
		{"iteration fails", "SELECT 1 UNION ALL SELECT abs(-9223372036854775808)", "integer overflow", nil},
		{"scan fails", "SELECT 'not a number'", "invalid syntax", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := queryAll(s, "collecting numbers", scan, tc.query)
			if tc.problem != "" {
				if err == nil || !strings.Contains(err.Error(), tc.problem) {
					t.Fatalf("got %v, %v; want error containing %q", got, err, tc.problem)
				}
			} else if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
			if s.db.Stats().InUse != 0 {
				t.Fatal("the cursor still owns the store's only connection")
			}
		})
	}
	sentinel := errors.New("scan stopped")
	_, err := queryAll(s, "collecting numbers", func(*sql.Rows) (int, error) {
		return 0, sentinel
	}, "SELECT 1")
	if !errors.Is(err, sentinel) {
		t.Fatalf("scanner error identity was lost: %v", err)
	}
}
