package store

import (
	"reflect"
	"testing"
)

func TestDialectExpressionsPreserveNullsAndSubstringBoundaries(t *testing.T) {
	s := newTestStore(t)
	for _, tc := range []struct {
		name, query string
		want        []any
	}{
		{"null aggregates", `SELECT SUM(x = 1), MAX(x = 1) FROM (SELECT CAST(NULL AS INTEGER) AS x) input`, []any{nil, nil}},
		{"null scalar minimum", `SELECT MIN(NULL, 5), MAX(3, NULL)`, []any{nil, nil}},
		{"substring zero", `SELECT SUBSTR('abcdef', 0, 3)`, []any{"ab"}},
		{"substring negative outside", `SELECT SUBSTR('abcdef', -9, 5)`, []any{"ab"}},
		{"substring negative count", `SELECT SUBSTR('abcdef', 3, -5)`, []any{"ab"}},
		{"substring null count", `SELECT SUBSTR('abcdef', 3, NULL)`, []any{nil}},
		{"substring remainder", `SELECT SUBSTR('abcdef', -3)`, []any{"def"}},
		{"literal syntax", `SELECT '?1 GROUP_CONCAT(a) IS ?'`, []any{"?1 GROUP_CONCAT(a) IS ?"}},
		{"integer width", `SELECT CAST(3000000000 AS INTEGER)`, []any{int64(3000000000)}},
		{"blob byte length", `SELECT LENGTH(CAST('\x41' AS BLOB))`, []any{int64(4)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]any, len(tc.want))
			dest := make([]any, len(got))
			for i := range got {
				dest[i] = &got[i]
			}

			if err := s.db.QueryRow(tc.query).Scan(dest...); err != nil {
				t.Fatal(err)
			}
			for i, value := range got {
				if bytes, ok := value.([]byte); ok {
					got[i] = string(bytes)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestPostgresGlobTranslationSurvivesParameterCompaction(t *testing.T) {
	s := newTestStore(t)
	for _, value := range []string{"ABC123", "not-a-commit"} {
		var commit bool
		if err := s.db.QueryRow(`SELECT ? NOT GLOB '*[^0-9a-fA-F]*'`, value).Scan(&commit); err != nil {
			t.Fatal(err)
		}
		if commit != (value == "ABC123") {
			t.Fatalf("%q classified as commit characters=%t", value, commit)
		}
	}
}
