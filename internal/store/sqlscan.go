package store

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func sqlDigit(c byte) bool { return c >= '0' && c <= '9' }

func scanSQLToken(s string, i int) (byte, int, error) {
	r, width := utf8.DecodeRuneInString(s[i:])
	switch {
	case unicode.IsSpace(r):
		return 0, i + width, nil
	case strings.HasPrefix(s[i:], "--"):
		end := strings.IndexByte(s[i:], '\n')
		if end < 0 {
			return 0, len(s), nil
		}
		return 0, i + end + 1, nil
	case strings.HasPrefix(s[i:], "/*"):
		end, err := scanSQLComment(s, i)
		return 0, end, err
	case sqlQuoteStart(s, i):
		end, err := scanSQLQuote(s, i)
		return 'q', end, err
	case s[i] == '$' && i+1 < len(s) && !sqlDigit(s[i+1]):
		end, err := scanSQLDollarQuote(s, i)
		return 'q', end, err
	case s[i] == '?' || s[i] == '$':
		return 'p', scanSQLDigits(s, i+1), nil
	case unicode.IsLetter(r) || r == '_':
		return 'w', scanSQLWord(s, i), nil
	case sqlDigit(s[i]) || s[i] == '.' && i+1 < len(s) && sqlDigit(s[i+1]):
		return 's', scanSQLNumber(s, i), nil
	default:
		for _, op := range []string{"->>", "#>>", "!~*", "->", "#>", "!~", "~*", "::", "||", "<>", "!=", "<=", ">="} {
			if strings.HasPrefix(s[i:], op) {
				return 's', i + len(op), nil
			}
		}
		return 's', i + width, nil
	}
}

func scanSQLComment(s string, start int) (int, error) {
	i, depth := start+2, 1
	for i < len(s) && depth > 0 {
		switch {
		case strings.HasPrefix(s[i:], "/*"):
			depth++
			i += 2
		case strings.HasPrefix(s[i:], "*/"):
			depth--
			i += 2
		default:
			i++
		}
	}
	if depth != 0 {
		return 0, fmt.Errorf("unterminated SQL comment")
	}
	return i, nil
}

func sqlQuoteStart(s string, i int) bool {
	if s[i] == '\'' || s[i] == '"' || s[i] == '`' {
		return true
	}
	return (s[i] == 'E' || s[i] == 'e') && i+1 < len(s) && s[i+1] == '\''
}

func scanSQLQuote(s string, start int) (int, error) {
	i := start
	escaped := s[i] == 'E' || s[i] == 'e'
	if escaped {
		i++
	}
	quote := s[i]
	for i++; i < len(s); {
		if escaped && s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] != quote {
			i++
			continue
		}
		i++
		if i < len(s) && s[i] == quote {
			i++
			continue
		}
		return i, nil
	}
	return 0, fmt.Errorf("unterminated SQL quote")
}

func scanSQLWord(s string, start int) int {
	i := start
	for i < len(s) {
		r, width := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			break
		}
		i += width
	}
	return i
}

func scanSQLDollarQuote(s string, start int) (int, error) {
	end := scanSQLWord(s, start+1)
	if end >= len(s) || s[end] != '$' {
		return 0, fmt.Errorf("invalid SQL dollar quote")
	}
	tag := s[start : end+1]
	close := strings.Index(s[end+1:], tag)
	if close < 0 {
		return 0, fmt.Errorf("unterminated SQL dollar quote")
	}
	return end + 1 + close + len(tag), nil
}

func scanSQLDigits(s string, start int) int {
	i := start
	for i < len(s) && sqlDigit(s[i]) {
		i++
	}
	return i
}

func scanSQLNumber(s string, start int) int {
	end := scanSQLDigits(s, start)
	if end < len(s) && s[end] == '.' {
		end = scanSQLDigits(s, end+1)
	}
	if end >= len(s) || s[end] != 'e' && s[end] != 'E' {
		return end
	}
	exponent := end + 1
	if exponent < len(s) && (s[exponent] == '+' || s[exponent] == '-') {
		exponent++
	}
	next := scanSQLDigits(s, exponent)
	if next > exponent {
		return next
	}
	return end
}
