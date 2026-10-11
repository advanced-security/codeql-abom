package store

import (
	"fmt"
	"strings"
)

func adaptSQLFunction(name string, args, original [][]sqlToken) (string, error) {
	rendered := make([]string, len(args))
	for i, a := range args {
		rendered[i] = renderSQL(a)
	}
	switch name {
	case "LOWER":
		if len(rendered) == 1 {
			return "(LOWER(" + rendered[0] + `) COLLATE "C")`, nil
		}
	case "CAST":
		return adaptSQLCast(args), nil
	case "GROUP_CONCAT":
		return adaptSQLConcat(args, rendered)
	case "INSTR":
		if len(rendered) != 2 {
			return "", fmt.Errorf("unsupported INSTR arity")
		}
		return "strpos((" + rendered[0] + `) COLLATE "C", ` + rendered[1] + ")", nil
	case "SUBSTR":
		if len(rendered) != 2 && len(rendered) != 3 {
			return "", fmt.Errorf("unsupported SUBSTR arity")
		}
		return fmt.Sprintf("abom_substr%d(%s)", len(rendered), strings.Join(rendered, ", ")), nil
	case "JSON_EACH":
		return `LATERAL (SELECT key COLLATE "C" AS key, value COLLATE "C" AS value,
			type COLLATE "C" AS type FROM abom_json_each(` + strings.Join(rendered, ", ") + `))`, nil
	case "JSON_EXTRACT":
		return `(abom_json_extract(` + strings.Join(rendered, ", ") + `) COLLATE "C")`, nil
	case "JSON_VALID", "JULIANDAY":
		return "abom_" + strings.ToLower(name) + "(" + strings.Join(rendered, ", ") + ")", nil
	case "SUM", "MIN", "MAX":
		return adaptSQLAggregate(name, rendered, original), nil
	}
	return "", nil
}

func adaptSQLCast(args [][]sqlToken) string {
	if len(args) != 1 || len(args[0]) < 3 {
		return ""
	}
	a := args[0]
	as := len(a) - 2
	if !tokenIs(a[as], "AS") {
		return ""
	}
	switch {
	case tokenIs(a[as+1], "INTEGER"):
		return "abom_integer(" + renderSQL(a[:as]) + ")"
	case tokenIs(a[as+1], "BLOB"):
		return "convert_to(" + renderSQL(a[:as]) + ", 'UTF8')"
	default:
		return ""
	}
}

func adaptSQLConcat(args [][]sqlToken, rendered []string) (string, error) {
	if len(args) < 1 || len(args) > 2 {
		return "", fmt.Errorf("unsupported GROUP_CONCAT arity")
	}
	sep := "','"
	if len(args) == 2 {
		sep = rendered[1]
	}
	expression, ordering := splitAggregateOrder(args[0])
	expr, distinct := renderSQL(expression), ""
	if len(expression) > 0 && tokenIs(expression[0], "DISTINCT") {
		expr, distinct = renderSQL(expression[1:]), "DISTINCT "
	}
	if len(ordering) > 0 {
		sep += " ORDER BY " + renderSQL(ordering)
	}
	return "string_agg(" + distinct + "(" + expr + ")::text, " + sep + ")", nil
}

func splitAggregateOrder(tokens []sqlToken) ([]sqlToken, []sqlToken) {
	depth := 0
	for i := 0; i+1 < len(tokens); i++ {
		if depth == 0 && startsWithSQL(tokens[i:], "ORDER", "BY") {
			return tokens[:i], tokens[i+2:]
		}
		switch tokens[i].text {
		case "(":
			depth++
		case ")":
			depth--
		}
	}
	return tokens, nil
}

func adaptSQLAggregate(name string, rendered []string, original [][]sqlToken) string {
	if len(rendered) > 1 && name != "SUM" {
		fn := "LEAST"
		if name == "MAX" {
			fn = "GREATEST"
		}
		nulls := make([]string, len(rendered))
		for i, a := range rendered {
			nulls[i] = "(" + a + ") IS NULL"
		}
		return "(CASE WHEN " + strings.Join(nulls, " OR ") +
			" THEN NULL ELSE " + fn + "(" + strings.Join(rendered, ", ") + ") END)"
	}
	if len(rendered) == 1 && predicateSQL(original[0]) {
		return name + "((" + rendered[0] + ")::integer)"
	}
	return ""
}
