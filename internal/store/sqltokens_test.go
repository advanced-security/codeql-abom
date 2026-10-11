package store

import (
	"strings"
	"testing"
)

func TestPostgresBindingLeavesQuotedParametersAndCommentsAlone(t *testing.T) {
	in := `SELECT '?1 '' ?2', "?9", $$ ?8 $$, $body$ ?7 $body$, ?2, ?, ?1 /* ?55 /* ?66 */ ?77 */ -- ?88
	, ?`
	got, err := postgresSQL(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`'?1 '' ?2'`, `"?9"`, `$$ ?8 $$`, `$body$ ?7 $body$`, `$2 , $3 , $1 , $4`} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q missing from %q", want, got)
		}
	}
	for _, bad := range []string{"$55", "$66", "$77", "$88"} {
		if strings.Contains(got, bad) {
			t.Fatalf("comment parameter rebound: %s", got)
		}
	}
}

func TestPostgresAdaptationsNeverRewriteLiteralSQL(t *testing.T) {
	got, err := postgresSQL(`SELECT 'GROUP_CONCAT(x) INSERT OR IGNORE ? GLOB', GROUP_CONCAT(DISTINCT a), INSTR(a, '/')
	FROM t WHERE a GLOB 'v[0-9]*'`)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`'GROUP_CONCAT(x) INSERT OR IGNORE ? GLOB'`, `string_agg`, `strpos`, `~ '^v[0-9].*$'`} {
		if !strings.Contains(got, want) {
			t.Fatalf("adaptation missing %q in %s", want, got)
		}
	}
}

func TestPostgresBindingPreservesEscapedAndNumericLiterals(t *testing.T) {
	query := `SELECT E'can\'t ?3', 1e-3, 2.50e+2, ?`
	got, err := postgresSQL(query)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`E'can\'t ?3'`, "1e-3", "2.50e+2", "$1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("literal %q changed in %s", want, got)
		}
	}
}
