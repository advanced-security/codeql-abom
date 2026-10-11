package store

import (
	"fmt"
	"strings"
)

func sqlFragment(text string) sqlToken { return sqlToken{text: text, kind: 's'} }

func matchingParen(t []sqlToken, start int) (int, error) {
	depth := 0
	for i := start; i < len(t); i++ {
		switch t[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("unbalanced SQL parentheses")
}

func sqlArguments(t []sqlToken) [][]sqlToken {
	var args [][]sqlToken
	start, depth := 0, 0
	for i, v := range t {
		switch v.text {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == 0 {
				args = append(args, t[start:i])
				start = i + 1
			}
		}
	}
	return append(args, t[start:])
}

func predicateSQL(t []sqlToken) bool {
	for len(t) >= 2 && t[0].text == "(" {
		end, err := matchingParen(t, 0)
		if err != nil || end != len(t)-1 {
			break
		}
		t = t[1:end]
	}
	depth := 0
	for _, v := range t {
		switch v.text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 {
			if tokenIs(v, "CASE") {
				return false
			}
			switch strings.ToUpper(v.text) {
			case "=", "<>", "!=", ">", "<", ">=", "<=", "IS", "AND", "OR", "NOT", "EXISTS", "IN", "LIKE", "GLOB":
				return true
			}
		}
	}
	return false
}

func adaptFunctions(t []sqlToken) ([]sqlToken, error) {
	var out []sqlToken
	for i := 0; i < len(t); i++ {
		if t[i].text != "(" {
			out = append(out, t[i])
			continue
		}
		end, err := matchingParen(t, i)
		if err != nil {
			return nil, err
		}
		name := ""
		if len(out) > 0 && out[len(out)-1].kind == 'w' {
			name = strings.ToUpper(out[len(out)-1].text)
		}
		original := sqlArguments(t[i+1 : end])
		inside, err := adaptFunctions(t[i+1 : end])
		if err != nil {
			return nil, err
		}
		args := sqlArguments(inside)
		replacement, err := adaptSQLFunction(name, args, original)
		if err != nil {
			return nil, err
		}
		if replacement != "" {
			out = out[:len(out)-1]
			out = append(out, sqlFragment(replacement))
		} else {
			out = append(out, t[i])
			out = append(out, inside...)
			out = append(out, t[end])
		}
		i = end
	}
	return out, nil
}

// These are the complete GLOB patterns used by the store; a new pattern requires a test.
var postgresGlob = map[string]string{
	"'v[0-9]*'":        "'^v[0-9].*$'",
	"'[0-9]*'":         "'^[0-9].*$'",
	"'[0-9a-f]*'":      "'^[0-9a-f].*$'",
	"'*[^0-9a-fA-F]*'": "'[^0-9a-fA-F]'",
	"'refs/tags/*'":    "'^refs/tags/.*$'",
	"'refs/heads/*'":   "'^refs/heads/.*$'",
}

func adaptStatement(t []sqlToken) ([]sqlToken, error) {
	t, err := adaptStatementPrefix(t)
	if err != nil {
		return nil, err
	}
	var out []sqlToken
	for i := 0; i < len(t); {
		var consumed int
		out, consumed, err = adaptSQLOperator(t[i:], out)
		if err != nil {
			return nil, err
		}
		i += consumed
	}
	return adaptFunctions(adaptNullOrdering(out))
}

// SQLite puts NULL first ascending and last descending; PostgreSQL's defaults are reversed.
func adaptNullOrdering(t []sqlToken) []sqlToken {
	insert := map[int]sqlToken{}
	finish := func(start, end int) {
		if start >= end || end > len(t) {
			return
		}
		expression := t[start:end]
		last := len(expression) - 1
		if len(expression) >= 2 && tokenIs(expression[last-1], "NULLS") {
			return
		}
		text := "NULLS FIRST"
		if tokenIs(expression[last], "DESC") {
			text = "NULLS LAST"
		}
		insert[end] = sqlFragment(text)
	}
	for i := 0; i+1 < len(t); i++ {
		if !tokenIs(t[i], "ORDER") || !tokenIs(t[i+1], "BY") {
			continue
		}
		start, depth := i+2, 0
		j := start
		for ; j < len(t); j++ {
			end := false
			comma := false
			if depth == 0 {
				comma = t[j].text == ","
				switch strings.ToUpper(t[j].text) {
				case ")", "LIMIT", "OFFSET", "FETCH", "UNION", "EXCEPT", "INTERSECT", "FOR", "ROWS", "RANGE", "GROUPS":
					end = true
				}
			}
			if end || comma {
				finish(start, j)
				start = j + 1
			}
			if end {
				break
			}
			if t[j].text == "(" {
				depth++
			} else if t[j].text == ")" {
				depth--
			}
		}
		if j == len(t) {
			finish(start, j)
		}
	}
	out := make([]sqlToken, 0, len(t)+len(insert))
	for i, token := range t {
		if v, ok := insert[i]; ok {
			out = append(out, v)
		}
		out = append(out, token)
	}
	if v, ok := insert[len(t)]; ok {
		out = append(out, v)
	}
	return out
}
