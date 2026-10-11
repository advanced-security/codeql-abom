package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func queryRunType(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name == "QueryRun"
	case *ast.SelectorExpr:
		return v.Sel.Name == "QueryRun"
	case *ast.StarExpr:
		return queryRunType(v.X)
	}
	return false
}

func runHasMode(lit *ast.CompositeLit) bool {
	for _, element := range lit.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if ok && key.Name == "AnalysisMode" {
			return true
		}
	}
	return false
}

func queryRunsMissingMode(filename string, body []byte) ([]token.Position, error) {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, filename, body, 0)
	if err != nil {
		return nil, err
	}
	var missing []token.Position
	check := func(lit *ast.CompositeLit) {
		if !runHasMode(lit) {
			missing = append(missing, positions.Position(lit.Pos()))
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if queryRunType(lit.Type) {
			check(lit)
		} else {
			checkImplicitRuns(lit, check)
		}
		return true
	})
	return missing, nil
}

func checkImplicitRuns(container *ast.CompositeLit, check func(*ast.CompositeLit)) {
	var elementType ast.Expr
	switch kind := container.Type.(type) {
	case *ast.MapType:
		elementType = kind.Value
	case *ast.ArrayType:
		elementType = kind.Elt
	}
	if !queryRunType(elementType) {
		return
	}
	for _, element := range container.Elts {
		if pair, ok := element.(*ast.KeyValueExpr); ok {
			element = pair.Value
		}
		if lit, ok := element.(*ast.CompositeLit); ok && lit.Type == nil {
			check(lit)
		}
	}
}

func TestShapeGuardDistinguishesContainersFromActualRunConstruction(t *testing.T) {
	for _, tc := range []struct {
		expression string
		missing    int
	}{
		{`map[string]store.QueryRun{"edges": planned.queryRun(query, at)}`, 0},
		{`store.QueryRun{QueryID: "edges"}`, 1},
		{`store.QueryRun{AnalysisMode: store.AnalysisModeRegular}`, 0},
		{`store.QueryRun{QueryID: "edges" /* AnalysisMode is not a field */}`, 1},
		{`map[string]store.QueryRun{"edges": {QueryID: "edges"}}`, 1},
		{`[]store.QueryRun{{AnalysisMode: regular}, {QueryID: "edges"}}`, 1},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			got, err := queryRunsMissingMode("fixture.go", []byte("package fixture\nvar v = "+tc.expression))
			if err != nil || len(got) != tc.missing {
				t.Fatalf("unattributed literals=%v error=%v, want %d", got, err, tc.missing)
			}
		})
	}
}
