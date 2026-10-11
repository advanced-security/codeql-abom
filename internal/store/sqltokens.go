package store

import (
	"fmt"
	"strconv"
	"strings"
)

type sqlToken struct {
	text string
	kind byte // w: word, q: quoted text, p: parameter, s: punctuation
}

func tokenIs(t sqlToken, text string) bool {
	return t.kind == 'w' && strings.EqualFold(t.text, text)
}

// Lexing shields literals, quoted identifiers, nested comments and dollar quotes from rewriting.
func lexSQL(s string) ([]sqlToken, error) {
	var out []sqlToken
	for i := 0; i < len(s); {
		kind, end, err := scanSQLToken(s, i)
		if err != nil {
			return nil, err
		}
		if kind != 0 {
			out = append(out, sqlToken{s[i:end], kind})
		}
		i = end
	}
	return out, nil
}

func renderSQL(tokens []sqlToken) string {
	parts := make([]string, len(tokens))
	for i, t := range tokens {
		parts[i] = t.text
	}
	return strings.Join(parts, " ")
}

func bindPostgres(tokens []sqlToken) ([]sqlToken, error) {
	next := 0
	for i := range tokens {
		t := &tokens[i]
		if t.kind != 'p' || !strings.HasPrefix(t.text, "?") {
			continue
		}
		n := next + 1
		if len(t.text) > 1 {
			var err error
			n, err = strconv.Atoi(t.text[1:])
			if err != nil || n < 1 {
				return nil, fmt.Errorf("invalid SQL parameter %q", t.text)
			}
		}
		if n > next {
			next = n
		}
		t.text = "$" + strconv.Itoa(n)
	}
	return tokens, nil
}

func postgresSQL(query string) (string, error) {
	tokens, err := lexSQL(query)
	if err != nil {
		return "", err
	}
	tokens, err = bindPostgres(tokens)
	if err != nil {
		return "", err
	}
	var out []string
	start, depth := 0, 0
	for i := 0; i <= len(tokens); i++ {
		if i == len(tokens) || tokens[i].text == ";" && depth == 0 {
			if start < i {
				t, err := adaptStatement(tokens[start:i])
				if err != nil {
					return "", err
				}
				out = append(out, renderSQL(t))
			}
			start = i + 1
			continue
		}
		if tokens[i].text == "(" {
			depth++
		} else if tokens[i].text == ")" {
			depth--
		}
	}
	return strings.Join(out, "; "), nil
}
