package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestProductionStorageContractsAreInterfaces(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[Backend](),
		reflect.TypeFor[GraphWriter](),
		reflect.TypeFor[Frontier](),
		reflect.TypeFor[ResponseCache](),
		reflect.TypeFor[DerivationWriter](),
	} {
		if typ.Kind() != reflect.Interface {
			t.Errorf("%s is %s, not a backend-neutral interface", typ, typ.Kind())
			continue
		}
		for _, problem := range contractTypeViolations(typ, map[reflect.Type]bool{}) {
			t.Errorf("%s: %s", typ, problem)
		}
	}
}

func contractTypeViolations(typ reflect.Type, seen map[reflect.Type]bool) []string {
	if seen[typ] {
		return nil
	}
	seen[typ] = true
	if strings.HasPrefix(typ.PkgPath(), "database/sql") {
		return []string{"leaks " + typ.String()}
	}
	var problems []string
	visit := func(child reflect.Type) {
		problems = append(problems, contractTypeViolations(child, seen)...)
	}
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		visit(m.Type)
	}
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
		visit(typ.Elem())
	case reflect.Map:
		visit(typ.Key())
		visit(typ.Elem())
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			visit(typ.Field(i).Type)
		}
	case reflect.Func:
		for i := 0; i < typ.NumIn(); i++ {
			visit(typ.In(i))
		}
		for i := 0; i < typ.NumOut(); i++ {
			visit(typ.Out(i))
		}
	}
	return problems
}

type concreteCacheContract interface{ ResponseCache() *HTTPCache }
type concreteWriterContract interface{ NewWriter(int) *Writer }

func TestTheContractGuardRejectsConcreteStorageBehindReturnTypes(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeFor[concreteCacheContract](), reflect.TypeFor[concreteWriterContract]()} {
		if len(contractTypeViolations(typ, map[reflect.Type]bool{})) == 0 {
			t.Errorf("accepted a concrete implementation behind %s", typ)
		}
	}
}

func TestProductionSQLCannotEscapeTheStore(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasPrefix(path, filepath.Join(root, "internal", "store")+string(filepath.Separator)) {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, problem := range storageViolations(f) {
			t.Errorf("%s: %s", path, problem)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

}

func storageViolations(f *ast.File) []string {
	var problems []string
	storeName := "store"
	for _, imp := range f.Imports {
		name, _ := strconv.Unquote(imp.Path.Value)
		if strings.HasPrefix(name, "database/sql") || strings.Contains(name, "sqlite") ||
			strings.Contains(name, "/pgx") || name == "github.com/lib/pq" {
			problems = append(problems, "imports backend-specific "+name)
		}
		if strings.HasSuffix(name, "/internal/store") && imp.Name != nil {
			storeName = imp.Name.Name
			if storeName == "." {
				problems = append(problems, "dot imports conceal concrete store bypasses")
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			value, _ := strconv.Unquote(lit.Value)
			if sqlStatement.MatchString(value) {
				problems = append(problems, "contains SQL outside the store")
			}
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "DB" {
			problems = append(problems, "accesses a raw database handle")
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == storeName {
			switch sel.Sel.Name {
			case "Store", "Open", "Writer", "NewWriter", "Queue", "HTTPCache":
				problems = append(problems, "bypasses the storage contract through "+sel.Sel.Name)
			}
		}
		return true
	})
	return problems
}

var sqlStatement = regexp.MustCompile(`(?is)^\s*(SELECT\b.+\bFROM\b|INSERT\s+(?:OR\s+\w+\s+)?INTO\b|DELETE\s+FROM\b|UPDATE\s+\w+\s+SET\b|PRAGMA\b|(?:CREATE|ALTER|DROP)\s+(?:TABLE|INDEX|VIEW)\b|WITH\b.+\bAS\s*\()`)

func TestTheStorageGuardRecognizesSQLAndConcreteBypasses(t *testing.T) {
	for name, source := range map[string]string{
		"SQL literal":           "package consumer\nconst query = `SELECT row_id FROM abom_node_live`",
		"transaction statement": "package consumer\nconst query = `DELETE FROM abom_exposure`",
		"raw driver":            "package consumer\nimport _ \"database/sql/driver\"",
		"dot import":            "package consumer\nimport . \"github.com/advanced-security/codeql-abom/internal/store\"",
		"aliased concrete type": "package consumer\nimport db \"github.com/advanced-security/codeql-abom/internal/store\"\nvar s *db.Store",
	} {
		t.Run(name, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), "consumer.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(storageViolations(f)) == 0 {
				t.Fatal("the guard accepted a production storage bypass")
			}
		})
	}
}
