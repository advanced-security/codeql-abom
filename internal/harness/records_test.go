package harness

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In a cluster the agent beside the worker can write the pack and the scratch, which the harness reads.
// A pipe there holds a read open, and a session's worker would wait on it, stopped or not, until its
// pod is killed with nothing it did landed; so the harness and what it calls read a file only as a
// regular file.
func TestTheHarnessReadsOnlyRegularFiles(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "authoring")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "os" &&
					(sel.Sel.Name == "Open" || sel.Sel.Name == "ReadFile") {
					t.Errorf("%s reads with os.%s; read through regular, which a pipe cannot hold open",
						fset.Position(sel.Pos()), sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// A session reaches the store only through Records, so a worker can run the same code against a
// snapshot of what the session reads and a journal of what it writes. A field, parameter or value
// typed as the store anywhere else would be a write the journal never sees, and a read of a store a
// worker does not have.
func TestTheHarnessReachesTheStoreOnlyThroughRecords(t *testing.T) {
	for _, dir := range []string{".", filepath.Join("..", "authoring")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if ok && pkg.Name == "store" && (sel.Sel.Name == "Backend" || sel.Sel.Name == "Store") {
					t.Errorf("%s names store.%s; a session reaches the store through Records",
						fset.Position(sel.Pos()), sel.Sel.Name)
				}
				return true
			})
		}
	}
}
