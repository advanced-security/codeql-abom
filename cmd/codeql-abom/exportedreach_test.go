package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// An exported function in internal/ that only its own tests call is dead code with a witness.
//
// `TestEveryInternalPackageIsReachableFromABinary` catches a whole package nobody reaches, and
// `unused` catches unexported symbols nothing calls. Between them sits a gap neither sees: an
// exported symbol in an internal package, which `unused` assumes is API and leaves alone, and
// which a package-level reachability check counts as reached because a sibling file is reached.
// internal/ has no external consumers, so "exported" there means nothing except that a test can
// see it.
//
// Functions only. Methods are excluded deliberately: one satisfying an interface has no textual
// caller, so flagging them would report the working code and not the dead code.
func TestNoExportedFunctionInInternalIsOnlyUsedByTests(t *testing.T) {
	root := repoRoot(t)

	declared := map[string]string{} // name -> file that declares it
	usedInProduction := map[string]bool{}

	for _, dir := range []string{"internal", "cmd"} {
		inInternal := dir == "internal"
		forEachGoFile(t, filepath.Join(root, dir), func(path string, file *ast.File) {
			if strings.HasSuffix(path, "_test.go") {
				return
			}

			// The declaration's own name is not a use of it. Skipping by *identifier* rather
			// than by file matters: `Batch` and `OrgKey` are both called from the same file that
			// declares them, and excluding the whole file reported two live functions as dead.
			names := map[*ast.Ident]bool{}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !fn.Name.IsExported() {
					continue
				}
				names[fn.Name] = true
				if inInternal {
					declared[fn.Name.Name] = path
				}
			}

			ast.Inspect(file, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.SelectorExpr:
					usedInProduction[v.Sel.Name] = true
				case *ast.Ident:
					if !names[v] {
						usedInProduction[v.Name] = true
					}
				}
				return true
			})
		})
	}

	var orphans []string
	for name, file := range declared {
		if !usedInProduction[name] {
			orphans = append(orphans, name+" ("+strings.TrimPrefix(file, root+"/")+")")
		}
	}
	sort.Strings(orphans)

	if len(orphans) > 0 {
		t.Errorf("%d exported function(s) in internal/ that no production code calls:\n\t%s\n"+
			"Unexport it, delete it, or give it a caller. internal/ has no consumers outside this "+
			"module, so an exported function only tests reach is dead code that reviews as API.",
			len(orphans), strings.Join(orphans, "\n\t"))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for dir != "/" {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("no go.mod above the working directory")
	return ""
}

func forEachGoFile(t *testing.T, root string, fn func(path string, file *ast.File)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Directories holding no Go source of this module: git metadata, the npm and .NET trees,
		// and `.abom`, which since `make test` points TMPDIR inside the checkout also holds the
		// toolchain's build scratch. Those come and go while the walk is running.
		if d.IsDir() && path != root {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" ||
				name == "bin" || name == "obj" {
				return filepath.SkipDir
			}
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		fn(path, parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}
