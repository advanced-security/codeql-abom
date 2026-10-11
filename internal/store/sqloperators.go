package store

import (
	"fmt"
	"strings"
)

func startsWithSQL(t []sqlToken, words ...string) bool {
	if len(t) < len(words) {
		return false
	}
	for i, word := range words {
		if !tokenIs(t[i], word) {
			return false
		}
	}
	return true
}

func adaptStatementPrefix(t []sqlToken) ([]sqlToken, error) {
	if startsWithSQL(t, "CREATE", "VIEW", "IF", "NOT", "EXISTS") {
		t = append([]sqlToken{sqlFragment("CREATE OR REPLACE VIEW")}, t[5:]...)
	}
	if len(t) > 4 && startsWithSQL(t, "DROP", "VIEW", "IF", "EXISTS") &&
		strings.HasPrefix(t[4].text, "abom_") {
		t = append(t, sqlFragment("CASCADE"))
	}
	ignore := startsWithSQL(t, "INSERT", "OR", "IGNORE")
	replace := startsWithSQL(t, "INSERT", "OR", "REPLACE")
	if replace && (len(t) < 5 || !tokenIs(t[4], "abom_tier_configured")) {
		return nil, fmt.Errorf("unsupported PostgreSQL INSERT OR REPLACE")
	}
	if ignore || replace {
		t = append(t[:1], t[3:]...)
		t = append(t, sqlFragment("ON CONFLICT DO NOTHING"))
	}
	return t, nil
}

func adaptSQLOperator(t, out []sqlToken) ([]sqlToken, int, error) {
	v := t[0]
	if v.kind != 'w' {
		return append(out, v), 1, nil
	}
	switch strings.ToUpper(v.text) {
	case "PRAGMA":
		return nil, 0, fmt.Errorf("SQLite PRAGMA is not a PostgreSQL capability")
	case "GLOB":
		return adaptSQLGlob(t, out)
	case "CROSS":
		if startsWithSQL(t, "CROSS", "JOIN") && joinHasCondition(t[2:]) {
			return out, 1, nil
		}
	case "COLLATE":
		return adaptSQLCollation(t, out)
	case "LIKE":
		v.text = "ILIKE"
		if len(t) > 1 && (t[1].kind == 'p' || t[1].kind == 'q') {
			t[1] = sqlFragment("(" + t[1].text + ` COLLATE "C")`)
		}
	case "IS":
		replacement, consumed := adaptSQLIs(t)
		return append(out, replacement), consumed, nil
	}
	return append(out, v), 1, nil
}

func joinHasCondition(t []sqlToken) bool {
	for _, token := range t {
		if tokenIs(token, "ON") {
			return true
		}
		if tokenIs(token, "JOIN") || tokenIs(token, "WHERE") || token.text == ")" {
			return false
		}
	}
	return false
}

func adaptSQLGlob(t, out []sqlToken) ([]sqlToken, int, error) {
	if len(t) < 2 || postgresGlob[t[1].text] == "" {
		return nil, 0, fmt.Errorf("unsupported PostgreSQL GLOB pattern")
	}
	op := "~"
	if len(out) > 0 && tokenIs(out[len(out)-1], "NOT") {
		out = out[:len(out)-1]
		op = "!~"
	}
	return append(out, sqlFragment(op), sqlFragment(postgresGlob[t[1].text])), 2, nil
}

func adaptSQLCollation(t, out []sqlToken) ([]sqlToken, int, error) {
	if len(t) > 1 {
		if tokenIs(t[1], "NOCASE") {
			return append(out, sqlFragment(`COLLATE "abom_nocase"`)), 2, nil
		}
		if tokenIs(t[1], "BINARY") {
			return append(out, sqlFragment(`COLLATE "C"`)), 2, nil
		}
	}
	return append(out, t[0]), 1, nil
}

func adaptSQLIs(t []sqlToken) (sqlToken, int) {
	at, replacement := 1, "IS NOT DISTINCT FROM"
	if len(t) > at && tokenIs(t[at], "NOT") {
		at, replacement = 2, "IS DISTINCT FROM"
	}
	if len(t) <= at {
		return t[0], 1
	}
	for _, word := range []string{"NULL", "TRUE", "FALSE", "DISTINCT"} {
		if tokenIs(t[at], word) {
			return t[0], 1
		}
	}
	return sqlFragment(replacement), at
}
